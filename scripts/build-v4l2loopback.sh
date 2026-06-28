#!/bin/bash
# Build v4l2loopback from upstream (v0.15.3 supports kernel 6.18; the apt
# 0.12.7 does not). Creates /dev/video20 as a single-planar loopback that
# ustreamer can read, fed from the multiplanar camss node by gstreamer.
set -e
KVER="${RADKVM_KVER:-$(uname -r)}"   # override for image builds (chroot uname is wrong)
KDIR=/lib/modules/$KVER/build

echo "== remove the failed apt DKMS package =="
sudo apt-get purge -y v4l2loopback-dkms 2>/dev/null \
  || sudo dpkg --remove --force-all v4l2loopback-dkms 2>/dev/null || true

echo "== ensure git =="
command -v git >/dev/null || sudo apt-get install -y git

echo "== fetch + build v4l2loopback v0.15.3 =="
cd "$HOME"
rm -rf v4l2loopback-src
git clone --depth=1 --branch v0.15.3 https://github.com/umlaeute/v4l2loopback.git v4l2loopback-src
cd v4l2loopback-src
make -C "$KDIR" M="$PWD" modules

echo "== install =="
sudo make -C "$KDIR" M="$PWD" modules_install
sudo depmod -a "$KVER"

if [ -n "${RADKVM_IMAGE_BUILD:-}" ]; then
  echo ">>> image build: installed for $KVER (not loading; autoloads on the target at boot)"
  exit 0
fi

echo "== load =="
sudo modprobe v4l2loopback video_nr=20 card_label=q6a-kvm exclusive_caps=1
echo "== verify =="
if lsmod | grep -q v4l2loopback; then
  echo ">>> LOADED"
  ls -l /dev/video20
  v4l2-ctl -d /dev/video20 --info | grep -iE 'Card type|Driver|Capabilities|Device Caps'
else
  echo ">>> NOT LOADED — paste: dmesg | tail -20"
fi
