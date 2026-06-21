#!/bin/bash
# RAD-KVM installer — Radxa Dragon Q6A (RadxaOS, kernel 6.18.2-3-qcom).
#
# Turns a stock RadxaOS Q6A into a working RAD-KVM: HDMI capture (C790/TC358743
# over CAM2) + USB HID/MSD gadget + browser control plane. Run from a clone of
# this repo:
#
#     git clone https://github.com/AriBerisha/rad-kvm && cd rad-kvm
#     sudo ./install.sh
#
# then REBOOT (the DT overlays + kernel module only take effect after a reboot;
# all services auto-start and self-configure on boot). Idempotent — safe to
# re-run. Builds everything from source on-device (needs internet).
set -euo pipefail

REPO="$(cd "$(dirname "$0")" && pwd)"
# RADKVM_KVER lets the image builder target the IMAGE's kernel from inside a
# chroot (where `uname -r` returns the build host's kernel, not the image's).
KVER="${RADKVM_KVER:-$(uname -r)}"
KDIR="/lib/modules/$KVER/build"
ESP="/boot/efi/RadxaOS/$KVER"
# the systemd-boot loader entry filename varies (entry-token/machine-id); find it,
# falling back to the RadxaOS-<kver> name.
ENTRY="$(ls /boot/efi/loader/entries/*.conf 2>/dev/null | grep -E "$KVER" | head -1)"
[ -n "$ENTRY" ] || ENTRY="/boot/efi/loader/entries/RadxaOS-$KVER.conf"
CAM_OVL="q6a-tc358743"
USB_OVL="qcs6490-radxa-dragon-q6a-usb-peripheral"

log()  { printf '\n\033[1;36m=== %s ===\033[0m\n' "$*"; }
warn() { printf '\033[1;33mWARN: %s\033[0m\n' "$*"; }
die()  { printf '\033[1;31mFATAL: %s\033[0m\n' "$*" >&2; exit 1; }

[ "$(id -u)" = 0 ] || die "run as root (sudo ./install.sh)"
[ -d "$KDIR" ] || die "no kernel headers at $KDIR — install the matching linux-headers package"
grep -qi 'dragon\|qcs6490' /sys/firmware/devicetree/base/model 2>/dev/null \
  || warn "this doesn't look like a Radxa Dragon Q6A — continuing anyway"

# ---------------------------------------------------------------------------
log "1/8  build dependencies"
apt-get update -y
apt-get install -y build-essential device-tree-compiler golang-go git curl \
  libevent-dev libbsd-dev libjpeg-dev pkg-config v4l-utils dosfstools \
  gstreamer1.0-tools gstreamer1.0-plugins-base gstreamer1.0-plugins-good \
  avahi-daemon libnss-mdns
# avahi-daemon + libnss-mdns: advertise the box over mDNS so it's reachable at
# <hostname>.local with no display / no IP hunting (headless discovery).

# ---------------------------------------------------------------------------
log "2/8  tc358743 kernel module (vendored, patched: link-freq + CAM_SENSOR + CCI I2C chunking)"
W="$(mktemp -d)"
cp "$REPO/kernel/tc358743/tc358743.c" "$REPO/kernel/tc358743/tc358743_regs.h" "$W/"
echo 'obj-m := tc358743.o' > "$W/Makefile"
grep -q I2C_BURST "$W/tc358743.c" || die "vendored tc358743.c missing the CCI I2C fix"
make -C "$KDIR" M="$W" modules
make -C "$KDIR" M="$W" modules_install
depmod -a "$KVER"
rm -rf "$W"

# ---------------------------------------------------------------------------
log "3/8  v4l2loopback + ustreamer (from source — apt versions are too old for k6.18)"
RADKVM_KVER="$KVER" bash "$REPO/scripts/build-v4l2loopback.sh"
bash "$REPO/scripts/build-ustreamer.sh"
command -v ustreamer >/dev/null || warn "ustreamer not on PATH after build — check scripts/build-ustreamer.sh"

# ---------------------------------------------------------------------------
log "4/8  control plane (qkvm)"
( cd "$REPO/controlplane" && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /usr/local/bin/qkvm . )

# ---------------------------------------------------------------------------
log "5/8  files: EDID, scripts, services, module configs"
install -d /etc/q6a-kvm /var/lib/q6a-kvm
install -m644 "$REPO/edid/rad-kvm.hex"                 /etc/q6a-kvm/edid.hex
install -m755 "$REPO/packaging/q6a-kvm-configure.sh"   /usr/local/bin/q6a-kvm-configure
install -m755 "$REPO/packaging/q6a-kvm-stream.sh"      /usr/local/bin/q6a-kvm-stream
install -m755 "$REPO/packaging/q6a-kvm-watchdog.sh"    /usr/local/bin/q6a-kvm-watchdog
install -m755 "$REPO/gadget/q6a-kvm-gadget.sh"         /usr/local/bin/q6a-kvm-gadget
install -m644 "$REPO/packaging/q6a-kvm-pipeline.service"     /etc/systemd/system/
install -m644 "$REPO/packaging/q6a-kvm-streamer.service"     /etc/systemd/system/
install -m644 "$REPO/packaging/q6a-kvm-watchdog.service"     /etc/systemd/system/
install -m644 "$REPO/gadget/q6a-kvm-gadget.service"          /etc/systemd/system/
install -m644 "$REPO/controlplane/q6a-kvm-controlplane.service" /etc/systemd/system/
install -m644 "$REPO/packaging/v4l2loopback.modprobe.conf" /etc/modprobe.d/q6a-kvm-v4l2loopback.conf
install -m644 "$REPO/packaging/v4l2loopback.load.conf"     /etc/modules-load.d/q6a-kvm-v4l2loopback.conf

# ---------------------------------------------------------------------------
log "6/8  device-tree: merge overlays INTO the base DTB"
# This board boots via systemd-boot, whose loader entry applies 'devicetree-overlay'
# only onto a base 'devicetree' — which RadxaOS's generated entry does NOT set, so
# overlays are silently dropped (no camera, no UDC). U-Boot/extlinux's fdtoverlays
# are likewise ignored here (it loads an embedded DTB). The robust fix that works on
# real hardware: pre-merge both overlays into the base DTB and hand systemd-boot that
# merged DTB as the base 'devicetree' (no runtime overlay application needed).
command -v fdtoverlay >/dev/null || apt-get install -y device-tree-compiler >/dev/null
dtc -@ -I dts -O dtb -o "/tmp/${CAM_OVL}.dtbo" "$REPO/kernel/dts/qcs6490-q6a-tc358743.dtso"
USBP="/boot/dtbo/${USB_OVL}.dtbo"; [ -f "$USBP" ] || USBP="/boot/dtbo/${USB_OVL}.dtbo.disabled"
[ -f "$USBP" ] || die "usb-peripheral overlay not found in /boot/dtbo"
BASE_DTB="$(find /usr/lib/linux-image-"$KVER" -name 'qcs6490-radxa-dragon-q6a.dtb' 2>/dev/null | head -1)"
[ -f "$BASE_DTB" ] || die "base DTB qcs6490-radxa-dragon-q6a.dtb not found under /usr/lib/linux-image-$KVER"

fdtoverlay -i "$BASE_DTB" -o /tmp/rad-kvm-merged.dtb "/tmp/${CAM_OVL}.dtbo" "$USBP" \
  || die "fdtoverlay merge failed"
fdtget /tmp/rad-kvm-merged.dtb /soc@0/usb@a600000 dr_mode 2>/dev/null | grep -q peripheral \
  || warn "merged DTB lacks usb@a600000 dr_mode=peripheral — the gadget may not come up"

if [ -f "$ENTRY" ]; then
  cp -f /tmp/rad-kvm-merged.dtb "$ESP/rad-kvm-merged.dtb"
  # set the merged DTB as the BASE devicetree; drop any devicetree-overlay line
  sed -i '/^devicetree-overlay/d' "$ENTRY"
  if grep -q '^devicetree ' "$ENTRY"; then
    sed -i "s|^devicetree .*|devicetree /RadxaOS/$KVER/rad-kvm-merged.dtb|" "$ENTRY"
  else
    printf 'devicetree /RadxaOS/%s/rad-kvm-merged.dtb\n' "$KVER" >> "$ENTRY"
  fi
  echo "  loader entry now:"; grep -E '^devicetree' "$ENTRY" | sed 's/^/    /'
else
  warn "systemd-boot loader entry $ENTRY not found — overlays won't apply; check your boot setup"
fi

# ---------------------------------------------------------------------------
log "7/8  enable services"
systemctl daemon-reload
systemctl enable q6a-kvm-pipeline.service q6a-kvm-streamer.service \
  q6a-kvm-watchdog.service q6a-kvm-gadget.service q6a-kvm-controlplane.service

# ---------------------------------------------------------------------------
log "8/8  done"
if [ -n "${RADKVM_IMAGE_BUILD:-}" ]; then
  echo "image-build mode: installed for kernel $KVER (no reboot)."
  exit 0
fi
ip=$(ip -o route get 1.1.1.1 2>/dev/null | sed -n 's/.*src \([0-9.]*\).*/\1/p')
cat <<EOF

RAD-KVM installed. REBOOT to apply the DT overlays + kernel module:

    sudo reboot

After reboot everything auto-starts and self-configures. Then:
  * Plug the target's HDMI into the C790, and the C790 ribbon into Q6A CAM2.
  * Plug the target's USB into the board's blue USB-A port (gadget side).
  * Open  http://${ip:-<board-ip>}:8000  -> "Create your account".

The target will read RAD-KVM's EDID and auto-pick a carryable mode
(<=720p60 / 1080p30 on the 2-lane link).
EOF
