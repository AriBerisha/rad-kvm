#!/bin/bash
# Build a slim, flashable RAD-KVM image from a stock RadxaOS Q6A image, using
# Docker on macOS (Apple Silicon — arm64 containers run native, so the chroot
# needs no emulation). Strips the GNOME desktop so the result is PiKVM-class.
#
#   ./image/build-image.sh <vendor-radxaos-q6a.img[.xz]> [out.img.xz]
#
# Output: a stripped, zero-filled, xz-compressed image to flash (Etcher / dd).
# Needs Docker running and ~16 GB free in the work dir (defaults under image/work,
# i.e. on the Mac's disk via a bind mount). First boot on the target re-expands
# the filesystem to fill the card.
set -euo pipefail
REPO="$(cd "$(dirname "$0")/.." && pwd)"
IN="${1:?usage: build-image.sh <vendor-radxaos.img[.xz]> [out.img.gz]}"
# Default to .gz: balenaEtcher's xz decompressor is unreliable ("writer process
# ended unexpectedly"), but it flashes gzip fine — as does Raspberry Pi Imager.
OUT="${2:-$REPO/image/rad-kvm-q6a.img.gz}"
WORK="${RADKVM_WORK:-$REPO/image/work}"
IMG="$WORK/rad-kvm-q6a.img"

command -v docker >/dev/null || { echo "Docker not found / not running"; exit 1; }
mkdir -p "$WORK"

echo "== prepare working image =="
case "$IN" in
  *.xz) echo "decompressing $IN ..."; xz -dc "$IN" > "$IMG" ;;
  *)    cp -f "$IN" "$IMG" ;;
esac
ls -lh "$IMG"

echo "== customize in a privileged arm64 container =="
docker run --rm --privileged \
  -v "$WORK":/work -v "$REPO":/repo:ro \
  arm64v8/ubuntu:24.04 /repo/image/customize-rootfs.sh /work/rad-kvm-q6a.img

echo "== compress -> $OUT (gzip; flashes directly in Etcher + Raspberry Pi Imager) =="
if command -v pigz >/dev/null; then pigz -9 -c "$IMG" > "$OUT"; else gzip -6 -c "$IMG" > "$OUT"; fi
ls -lh "$OUT"
echo
echo ">>> Flash $OUT with balenaEtcher or Raspberry Pi Imager (no decompression"
echo ">>>   needed), boot the Q6A on Ethernet, open http://rad-kvm.local:8000."
echo ">>> (work image left at $IMG — delete to reclaim space.)"
