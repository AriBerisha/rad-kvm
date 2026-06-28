# image — slim flashable RAD-KVM image

Builds a **PiKVM-class flashable image** from a stock RadxaOS Q6A image: bake in
RAD-KVM, strip the GNOME desktop + generic firmware (a KVM is headless), zero +
gzip → `rad-kvm-q6a.img.gz`. Flash, boot, set a password — **zero commands**.

This is the convenience tier on top of [`install.sh`](../install.sh); both share
the same install core, so people who already run RadxaOS use the installer and
everyone else flashes the image.

**Result (verified build):** vendor `radxa-dragon-q6a_noble_gnome` is a **1.5 GB**
download; the RAD-KVM image is **~1.1 GB** `.gz` — smaller *and* it ships the whole
stack preinstalled instead of a bare desktop OS.

> **Why `.gz`, not `.xz`?** balenaEtcher's bundled `xz` decompressor is unreliable
> — it fails with *"writer process ended unexpectedly / archive may be corrupted"*
> on perfectly valid `.xz` (verified here, both single- and multi-block). It flashes
> **gzip** fine, as does Raspberry Pi Imager, so `.gz` is the default distributable.
> (`xz` is ~770 MB and works in RasPi Imager / `dd`, but most users use Etcher.)

## Build (on an Apple-Silicon Mac with Docker)

```sh
./image/build-image.sh <vendor-radxaos-q6a.img.xz> [out.img.gz]
```

It decompresses the vendor image, runs a **privileged arm64 container** that
loop-mounts the image and `chroot`s into the rootfs (native arm64 → no qemu),
strips the desktop, runs `install.sh` in **image mode** (builds the `tc358743` /
`v4l2loopback` modules against the *image's* kernel via `RADKVM_KVER`, builds
`qkvm`, installs services + overlays — no reboot), removes the build toolchain,
zeroes free space, then gzip-compresses.

Needs: Docker running; **~7 GB** free work space (defaults to `image/work`, on the
Mac's disk via a bind mount); the vendor `.img.xz` (Radxa downloads — the GNOME
image is the only one shipped). Build time ≈ 10 min on an M-series Mac.

## What it strips (and why it's safe headless)

A KVM never uses a local display (the C790 is the *input*), so:

- **GNOME desktop** (`gnome-shell`, `gdm3`, `gnome-control-center`,
  `gnome-software`, `xserver-xorg-core`) + `firefox-esr` + CJK fonts + wallpapers
  — and `apt autoremove` cascades their libraries (webkit, mutter, …). ~130
  packages total.
- **`linux-firmware`** (533 MB) — generic blobs for hardware the Q6A doesn't have.
  The Qualcomm/Radxa firmware stays (`linux-firmware-dragonwing`,
  `radxa-firmware-qcs6490`) and WiFi is the separate `aic8800` package.

## Status — flash-tested on hardware ✅ (boot fix folded in)

The first real flash surfaced one bug, now fixed in `install.sh`: **this board boots
via systemd-boot, and RadxaOS's loader entry sets `devicetree-overlay` but no base
`devicetree`** — so the overlays were silently dropped (no `tc358743` camera, no USB
UDC → the gadget + qkvm crash-loop → `:8000` never comes up). U-Boot/extlinux's
`fdtoverlays` are ignored here too (it boots an embedded DTB), and replacing the
`/usr/lib` base DTB does nothing. The fix the build now bakes in: **`fdtoverlay`-merge
both overlays into the base DTB, drop it on the ESP, and set the loader entry's base
`devicetree` to it** (no runtime overlay application) — then `dr_mode=peripheral` →
UDC → `/dev/hidg0` → qkvm binds `:8000`, and the camera node exists.

Confirmed good on hardware in the same session:
- **Boots, DHCPs over Ethernet, reachable at `rad-kvm.local`** (mDNS works).
- **`linux-firmware` removal is safe** — boot + networking unaffected.
- **First-boot filesystem expand works** — the rootfs grew to fill the card on its own.
- Web UI serves `200` on `:8000` once the overlays apply.

Validate a build: flash `rad-kvm-q6a.img.gz` (Etcher or Raspberry Pi Imager), boot on
Ethernet, open `http://rad-kvm.local:8000`, create an account, check video + HID.

Optional further shrink: we zero free space (small *download*); to also shrink the
*flashed* size, add a `resize2fs` + partition-shrink + GPT-fixup step.
