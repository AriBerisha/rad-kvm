#!/bin/bash
# Build + install the patched tc358743 driver from the VENDORED source in this
# repo (kernel/tc358743/), which carries all the patches:
#   - V4L2_CID_LINK_FREQ control (camss reads the CSI-2 link rate)
#   - entity function MEDIA_ENT_F_CAM_SENSOR (camss sensor-pad walk finds it)
#   - I2C transfer chunking for Qualcomm CCI (so the EDID actually writes; CCI
#     rejects the mainline 128-byte block write with -EOPNOTSUPP -> empty EDID)
# Build against the running kernel; reboot to load (live rmmod is refused while
# camss is bound).
set -e
KVER="${RADKVM_KVER:-$(uname -r)}"   # override for image builds (chroot uname is wrong)
KDIR=/lib/modules/$KVER/build
HERE="$(cd "$(dirname "$0")" && pwd)"
SRC="$HERE/../kernel/tc358743"

echo "kernel=$KVER  build=$KDIR  src=$SRC"
[ -d "$KDIR" ] || { echo "FATAL: no kernel build dir at $KDIR"; exit 1; }
[ -f "$SRC/tc358743.c" ] || { echo "FATAL: vendored source not found at $SRC"; exit 1; }
grep -q I2C_BURST "$SRC/tc358743.c" || { echo "FATAL: source missing the CCI I2C-chunk fix"; exit 1; }

W=$(mktemp -d)
cp "$SRC/tc358743.c" "$SRC/tc358743_regs.h" "$W/"
echo 'obj-m := tc358743.o' > "$W/Makefile"

echo "== build =="
make -C "$KDIR" M="$W" modules
echo "== install + depmod =="
sudo make -C "$KDIR" M="$W" modules_install
sudo depmod -a
rm -rf "$W"

echo ">>> Installed. The running module is bound to camss, so reboot to load the new one:"
echo ">>>   sudo reboot"
echo ">>> After reboot verify the EDID writes (no -95):  v4l2-ctl -d <bridge> --get-edid"
