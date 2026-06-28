# RAD-KVM

Self-hosted **KVM-over-IP** on a single **Radxa Dragon Q6A**. See and drive a
headless machine from your browser — its **HDMI** becomes live video, your
keyboard and mouse become **USB**. No cloud, no account on someone's server,
nothing installed on the target.

> "KVM" here means **keyboard-video-mouse over IP**, not the Linux hypervisor.

## Get started

**Hardware:** a Radxa Dragon Q6A, a Geekworm **C790** (TC358743 HDMI→CSI bridge)
on the board's **CAM2** port, the target's HDMI into the C790, and the target's
USB into the board's blue **USB-A** port. Put the Q6A on your network by **Ethernet**.

### Option A — flash the image (no shell needed)

1. Download **[`rad-kvm-q6a.img.gz`](https://github.com/AriBerisha/rad-kvm/releases/latest)** (~1.1 GB).
2. Write it to an SD/eMMC card with **balenaEtcher** or **Raspberry Pi Imager** — flash the `.gz` directly, no need to unzip.
3. Boot the Q6A on Ethernet, then open **http://rad-kvm.local:8000** and set a password.

### Option B — install on an existing RadxaOS

```sh
git clone https://github.com/AriBerisha/rad-kvm && cd rad-kvm
sudo ./install.sh
sudo reboot
```

`install.sh` builds the driver, USB gadget and web UI from source on the board,
writes the boot config, and enables everything. After the one reboot it
self-configures — open **http://rad-kvm.local:8000**.

That's the whole setup. The target reads RAD-KVM's EDID and picks a carryable
mode on its own (≤ 720p60 / 1080p30 on the 2-lane link); video, keyboard, mouse,
Wake-on-LAN, shortcuts and macros are all in the browser.

## What you get

- Live screen of the target — HDMI capture → MJPEG
- Full keyboard + absolute mouse (USB HID gadget) and a virtual USB drive (mass-storage)
- Wake-on-LAN, one-click shortcuts, macros, type/paste
- Single-user login with optional **TOTP 2FA** (Google Authenticator et al.), mDNS discovery (`rad-kvm.local`), one dependency-free Go binary
- Headless image, no desktop, no telemetry

## How it works

```
target ──HDMI──▶ TC358743 ──CSI-2──▶  Q6A  ──MJPEG / LAN──▶ your browser :8000
   ▲                                                              │
   └──────────────────  USB HID gadget  ◀── your keyboard/mouse ──┘
```

Video out one way, control back the other, over one network. The target runs no
software and never knows it isn't a real monitor and keyboard.

## What it runs on

Tested on the **Radxa Dragon Q6A (QCS6490)**. Any board with a **MIPI CSI input**
(for the bridge) and a **USB OTG / peripheral** port could host it, but a
different SoC means the platform glue must be ported first — e.g. RK3588 ROCK
boards have the right ports but are **untested**.

## Repo layout

```
rad-kvm/
├── install.sh              # one-shot installer (Option B)
├── index.html              # project page
├── docs/
│   ├── architecture.md     # current system map (start here)
│   ├── hardware.md         # BOM, wiring, connector map
│   ├── handover.md         # founding spec
│   └── bringup-log.md      # running experiment log (newest first)
├── kernel/
│   ├── tc358743/           # vendored + patched bridge driver (see its README)
│   └── dts/                # CAM2 2-lane TC358743 overlay (+ vendor references)
├── scripts/                # build-tc358743.sh, build-ustreamer.sh, capture.sh …
├── gadget/                 # configfs USB composite gadget (HID + MSD) + service
├── controlplane/           # Go web UI + auth + HID bridge (binary `qkvm`)
├── edid/                   # capped EDID blobs (+ make-edid.py generator)
├── packaging/              # systemd services: pipeline, streamer, watchdog
└── image/                  # slim-image build recipe (Option A)
```

## How it was built

RAD-KVM is hand-written platform glue over PiKVM-style userspace. The interesting
parts — a vendored, patched `tc358743` driver (including a Qualcomm-CCI I2C fix so
the EDID actually writes), a device-tree merge the bootloader will honour, the
qcom-camss capture pipeline, and the Go control plane — plus every dead end along
the way, are written up here:

- [`docs/architecture.md`](docs/architecture.md) — current system map
- [`docs/hardware.md`](docs/hardware.md) — BOM, wiring, connectors
- [`docs/bringup-log.md`](docs/bringup-log.md) — running experiment log
- [`kernel/tc358743/README.md`](kernel/tc358743/README.md) — the driver patches

## License

GPL-3.0 — see [LICENSE](LICENSE). Built by hand on a Radxa Dragon Q6A; not
affiliated with Radxa or Qualcomm.
