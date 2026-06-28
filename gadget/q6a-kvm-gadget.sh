#!/bin/bash
# q6a-kvm USB composite gadget (PiKVM-class): HID keyboard + HID mouse (absolute
# and relative) + mass-storage, via configfs/libcomposite on the QCS6490 dwc3 UDC
# (a600000.usb, in peripheral mode via the usb-peripheral DT overlay).
#
# Idempotent: tears down an existing gadget of the same name, then rebuilds+binds.
# HID report descriptors match PiKVM/kvmd so kvmd's HID layer can drive them.
#   /dev/hidg0 = keyboard (8-byte boot report)
#   /dev/hidg1 = mouse ABSOLUTE (6 bytes: buttons, x:0-32767, y:0-32767, wheel)
#   /dev/hidg2 = mouse RELATIVE (4 bytes: buttons, dx, dy, wheel)
set -u

NAME=q6a-kvm
G=/sys/kernel/config/usb_gadget/$NAME
MSD_IMG=/var/lib/q6a-kvm/msd.img
MSD_MB=64

modprobe libcomposite 2>/dev/null || true
[ -d /sys/kernel/config/usb_gadget ] || { echo "configfs usb_gadget missing"; exit 1; }

# wait for the dwc3 peripheral UDC to appear (needs the usb-peripheral overlay)
for _i in $(seq 1 30); do [ -n "$(ls /sys/class/udc 2>/dev/null)" ] && break; sleep 1; done
[ -n "$(ls /sys/class/udc 2>/dev/null)" ] || { echo "no UDC in /sys/class/udc — is the usb-peripheral overlay applied (dr_mode=peripheral)?"; exit 1; }

# --- teardown existing (order matters in configfs) ---
if [ -d "$G" ]; then
  echo "" > "$G/UDC" 2>/dev/null || true
  for l in "$G"/configs/c.1/*; do [ -L "$l" ] && rm -f "$l"; done
  rmdir "$G"/configs/c.1/strings/0x409 2>/dev/null || true
  rmdir "$G"/configs/c.1 2>/dev/null || true
  for f in "$G"/functions/*; do [ -d "$f" ] && rmdir "$f" 2>/dev/null || true; done
  rmdir "$G"/strings/0x409 2>/dev/null || true
  rmdir "$G" 2>/dev/null || true
fi

hexto(){ local o=""; local b; for b in $1; do o="$o\\x$b"; done; printf "$o" > "$2"; }

mkdir -p "$G"; cd "$G"
echo 0x1d6b > idVendor          # Linux Foundation
echo 0x0104 > idProduct         # Multifunction Composite Gadget
echo 0x0100 > bcdDevice
echo 0x0200 > bcdUSB
echo 0xEF   > bDeviceClass       # Misc (Interface Association Descriptor)
echo 0x02   > bDeviceSubClass
echo 0x01   > bDeviceProtocol

mkdir -p strings/0x409
echo "q6a-kvm"              > strings/0x409/manufacturer
echo "q6a-kvm Composite KVM"> strings/0x409/product
echo "q6akvm0001"           > strings/0x409/serialnumber

mkdir -p configs/c.1/strings/0x409
echo "q6a-kvm" > configs/c.1/strings/0x409/configuration
echo 250  > configs/c.1/MaxPower
echo 0x80 > configs/c.1/bmAttributes      # bus-powered

# --- HID keyboard (hidg0) ---
mkdir -p functions/hid.usb0
echo 1 > functions/hid.usb0/protocol      # 1 = keyboard
echo 1 > functions/hid.usb0/subclass      # 1 = boot
echo 8 > functions/hid.usb0/report_length
hexto "05 01 09 06 a1 01 05 07 19 e0 29 e7 15 00 25 01 75 01 95 08 81 02 95 01 75 08 81 01 95 05 75 01 05 08 19 01 29 05 91 02 95 01 75 03 91 01 95 06 75 08 15 00 25 65 05 07 19 00 29 65 81 00 c0" functions/hid.usb0/report_desc

# --- HID mouse ABSOLUTE (hidg1) ---
mkdir -p functions/hid.usb1
echo 0 > functions/hid.usb1/protocol
echo 0 > functions/hid.usb1/subclass
echo 6 > functions/hid.usb1/report_length
hexto "05 01 09 02 a1 01 09 01 a1 00 05 09 19 01 29 05 15 00 25 01 95 05 75 01 81 02 95 01 75 03 81 01 05 01 09 30 09 31 16 00 00 26 ff 7f 75 10 95 02 81 02 09 38 15 81 25 7f 75 08 95 01 81 06 c0 c0" functions/hid.usb1/report_desc

# --- HID mouse RELATIVE (hidg2) ---
mkdir -p functions/hid.usb2
echo 0 > functions/hid.usb2/protocol
echo 0 > functions/hid.usb2/subclass
echo 4 > functions/hid.usb2/report_length
hexto "05 01 09 02 a1 01 09 01 a1 00 05 09 19 01 29 05 15 00 25 01 95 05 75 01 81 02 95 01 75 03 81 01 05 01 09 30 09 31 09 38 15 81 25 7f 75 08 95 03 81 06 c0 c0" functions/hid.usb2/report_desc

# --- Mass storage (removable, hot-swappable image) ---
mkdir -p /var/lib/q6a-kvm
if [ ! -f "$MSD_IMG" ]; then
  dd if=/dev/zero of="$MSD_IMG" bs=1M count="$MSD_MB" status=none
  command -v mkfs.vfat >/dev/null 2>&1 && mkfs.vfat "$MSD_IMG" >/dev/null 2>&1 || true
fi
mkdir -p functions/mass_storage.usb0
echo 1 > functions/mass_storage.usb0/stall
echo 0 > functions/mass_storage.usb0/lun.0/cdrom
echo 0 > functions/mass_storage.usb0/lun.0/ro
echo 1 > functions/mass_storage.usb0/lun.0/removable
echo "$MSD_IMG" > functions/mass_storage.usb0/lun.0/file

# --- link functions into the config (order => interface order) ---
ln -s functions/hid.usb0          configs/c.1/
ln -s functions/hid.usb1          configs/c.1/
ln -s functions/hid.usb2          configs/c.1/
ln -s functions/mass_storage.usb0 configs/c.1/

# --- bind to the UDC ---
UDC_NAME="$(ls /sys/class/udc | head -1)"
echo "$UDC_NAME" > UDC
echo "bound q6a-kvm gadget to UDC: $UDC_NAME"
echo "state: $(cat /sys/class/udc/"$UDC_NAME"/state 2>/dev/null)"
ls -l /dev/hidg* 2>/dev/null || echo "(no /dev/hidg* yet)"
