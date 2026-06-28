#!/bin/bash
# Runs INSIDE a privileged arm64 container (see build-image.sh). Customizes a
# RadxaOS .img in place: maps partitions, chroots into the rootfs, installs
# RAD-KVM for the IMAGE's kernel (image mode = no reboot), strips the desktop +
# build toolchain, then zeroes free space so it compresses small.
#
# Usage (inside container):  customize-rootfs.sh /work/rad-kvm-q6a.img
set -euo pipefail
IMG="${1:?usage: customize-rootfs.sh <image.img>}"
REPO=/repo
log(){ printf '\n\033[1;35m### %s\033[0m\n' "$*"; }

export DEBIAN_FRONTEND=noninteractive
log "container tools"
apt-get -qq update >/dev/null
apt-get -qq install -y kpartx dosfstools e2fsprogs zerofree util-linux >/dev/null

log "map partitions"
dmsetup remove_all 2>/dev/null || true   # clear stale dm/loop state in the VM kernel
losetup -D 2>/dev/null || true
kpartx -d "$IMG" 2>/dev/null || true
ADD=$(kpartx -av "$IMG"); echo "$ADD"; sync; sleep 1
[ -e "$(echo "$ADD" | awk '/add map/{print "/dev/mapper/"$3; exit}')" ] || { echo "FATAL: kpartx could not map (loop exhausted?)"; losetup -a; exit 1; }
# parse the actual mapped devices from -av output (do NOT call -l: it re-loops)
mapfile -t MAPS < <(printf '%s\n' "$ADD" | awk '/add map/{print "/dev/mapper/"$3}')
[ "${#MAPS[@]}" -ge 1 ] || { echo "no partitions mapped"; exit 1; }

ROOT=""; ESP=""
for p in "${MAPS[@]}"; do
  t=$(blkid -o value -s TYPE "$p" 2>/dev/null || true)
  sz=$(blockdev --getsize64 "$p" 2>/dev/null || echo 0)
  echo "  $p  type=${t:-?}  size=$((sz/1024/1024))MB"
  [ "$t" = ext4 ] && ROOT="$p"        # RadxaOS has a single ext4 rootfs
  [ "$t" = vfat ] && ESP="$p"
done
[ -n "$ROOT" ] || { echo "FATAL: no ext4 rootfs"; kpartx -d "$IMG"; exit 1; }
echo "rootfs=$ROOT  esp=${ESP:-none}"

log "mount + bind"
MNT=/mnt/rootfs; mkdir -p "$MNT"; mount "$ROOT" "$MNT"
[ -f "$MNT/etc/os-release" ] || { echo "FATAL: $ROOT isn't the rootfs"; umount "$MNT"; kpartx -d "$IMG"; exit 1; }
[ -n "$ESP" ] && { mkdir -p "$MNT/boot/efi"; mount "$ESP" "$MNT/boot/efi" || true; }
for d in proc sys dev dev/pts run; do mount --bind "/$d" "$MNT/$d" 2>/dev/null || true; done
cp -f /etc/resolv.conf "$MNT/etc/resolv.conf"

KVER=$(ls "$MNT/lib/modules" | sort -V | tail -1)
echo "image kernel: $KVER"
# RadxaOS ships the headers in /usr/src but may omit the build-dir symlink that
# out-of-tree `make` needs — create it so the module builds work.
if [ ! -e "$MNT/lib/modules/$KVER/build" ] && [ -d "$MNT/usr/src/linux-headers-$KVER" ]; then
  ln -sf "/usr/src/linux-headers-$KVER" "$MNT/lib/modules/$KVER/build"
  echo "  linked /lib/modules/$KVER/build -> /usr/src/linux-headers-$KVER"
fi

log "free space FIRST: strip the desktop (rootfs ships nearly full, 15M free)"
chroot "$MNT" /bin/bash -c "
  export DEBIAN_FRONTEND=noninteractive
  apt-get clean || true
  # bootstrap apt room: rm the biggest dir directly (apt can't run at 15M free)
  rm -rf /usr/lib/firefox-esr /usr/share/doc/* /var/cache/* /var/log/* /tmp/* /var/tmp/* /opt/rad-kvm-src 2>/dev/null || true
  find /usr/share/locale -mindepth 1 -maxdepth 1 -type d ! -name 'en*' ! -name 'C*' -exec rm -rf {} + 2>/dev/null || true
  echo '  free after bootstrap rm:'; df -h / | tail -1
  # purge the CONFIRMED-installed desktop packages; autoremove cascades the libs.
  # linux-firmware (533MB) = generic blobs for hw the Q6A lacks; the Qualcomm/Radxa
  # firmware is in linux-firmware-dragonwing + radxa-firmware-qcs6490 (kept), and
  # WiFi is the separate aic8800 package. Removing it is the biggest size win.
  apt-get purge -y firefox-esr gnome-shell gnome-shell-common gnome-shell-extensions \
    gnome-session gnome-session-bin gnome-session-common gdm3 \
    gnome-control-center gnome-control-center-data gnome-software gnome-software-common \
    xserver-xorg-core fonts-noto-cjk ubuntu-wallpapers-noble gnome-backgrounds \
    linux-firmware 2>/dev/null || true
  # heavy libs the GNOME image bundles that a headless KVM never uses — visualization
  # (VTK/OpenCV), geospatial (GDAL/proj-data) and software-GL (mesa/LLVM). Removing
  # them frees ~400MB so the on-device toolchain + Go build caches have room.
  apt-get purge -y 'libvtk9*' 'libopencv*' libgdal34t64 proj-data proj-bin \
    'mesa-*' 'libgl1-mesa*' 'libglx-mesa*' libllvm20 2>/dev/null || true
  apt-get autoremove --purge -y 2>/dev/null || true
  apt-get clean
" || true
echo "  rootfs free after desktop strip:"; df -h "$MNT" | tail -1

log "install RAD-KVM into the image (kernel $KVER, image mode)"
rm -rf "$MNT/opt/rad-kvm-src"; mkdir -p "$MNT/opt/rad-kvm-src"
tar -C "$REPO" --exclude=.git --exclude='image/work' --exclude='*.img' --exclude='*.img.xz' -cf - . | tar -C "$MNT/opt/rad-kvm-src" -xf -
chroot "$MNT" /bin/bash -eu -c "
  export DEBIAN_FRONTEND=noninteractive RADKVM_KVER='$KVER' RADKVM_IMAGE_BUILD=1
  /opt/rad-kvm-src/install.sh
"

log "remove build toolchain + clean"
chroot "$MNT" /bin/bash -c "
  export DEBIAN_FRONTEND=noninteractive
  apt-get purge -y build-essential 'gcc-1*' 'g++-1*' golang-go device-tree-compiler \
    'linux-headers-*' libevent-dev libbsd-dev libjpeg-dev 2>/dev/null || true
  apt-get autoremove --purge -y 2>/dev/null || true
  apt-get clean
" || true
rm -rf "$MNT/opt/rad-kvm-src" "$MNT/root/ustreamer-src" "$MNT/root/v4l2loopback-src" \
       "$MNT"/var/lib/apt/lists/* "$MNT"/var/log/* "$MNT"/tmp/* "$MNT"/var/tmp/* 2>/dev/null || true

log "set hostname rad-kvm (headless discovery: http://rad-kvm.local:8000)"
echo rad-kvm > "$MNT/etc/hostname"
if grep -q '^127.0.1.1' "$MNT/etc/hosts" 2>/dev/null; then
  sed -i 's/^127.0.1.1.*/127.0.1.1\trad-kvm/' "$MNT/etc/hosts"
else
  printf '127.0.1.1\trad-kvm\n' >> "$MNT/etc/hosts"
fi
chroot "$MNT" systemctl enable avahi-daemon 2>/dev/null || true

log "unmount"
sync
for d in run dev/pts dev sys proc; do umount -lf "$MNT/$d" 2>/dev/null || true; done
[ -n "$ESP" ] && umount -lf "$MNT/boot/efi" 2>/dev/null || true
umount -lf "$MNT" 2>/dev/null || true

log "fsck + zerofree (free space -> zeros -> compresses to ~nothing)"
e2fsck -fy "$ROOT" >/dev/null 2>&1 || true
zerofree "$ROOT" || echo "(zerofree skipped)"
kpartx -d "$IMG"
log "image customized OK"
