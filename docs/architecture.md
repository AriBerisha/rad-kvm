# q6a-kvm — architecture (current state)

A PiKVM-class **KVM-over-IP appliance** on the **Radxa Dragon Q6A** (Qualcomm
QCS6490). It captures a target machine's HDMI and presents keyboard / mouse /
mass-storage back to that target over USB, all driven from a browser. Both halves
— **video in** and **control out** — work on real hardware, behind a single-user
web UI we built (Go, binary `qkvm`).

This file is the **system map / orientation doc**. Other docs:
`docs/bringup-log.md` = newest-first experiment log (history + rationale);
`docs/handover.md` = founding spec; `docs/hardware.md` = BOM/wiring; component
READMEs in `gadget/`, `packaging/`, `controlplane/`.

## Data flow

```
VIDEO  (target -> browser)
  Target HDMI out
   -> TC358743 bridge (Geekworm C790)         HDMI -> MIPI CSI-2, does RGB->UYVY
   -> control I2C on Qualcomm CCI (18-000f)
   -> CSIPHY2 -> CSID0 -> VFE0 RDI0 (qcom-camss, 2-lane CAM2)
   -> /dev/video0          UYVY 4:2:2, multiplanar
   -> gst feeder           v4l2src ! v4l2sink
   -> /dev/video20         v4l2loopback, single-planar
   -> ustreamer            CPU MJPEG  ->  http://<board>:8080/stream
   -> qkvm reverse-proxy   (auth-gated)  ->  browser <img src="/stream">

CONTROL  (browser -> target)
  Browser key/mouse/wheel events
   -> WebSocket /ws/input   (auth-gated)
   -> qkvm HID bridge       JS event.code -> USB HID usage; builds reports
   -> /dev/hidg0 (keyboard) /dev/hidg1 (abs mouse) /dev/hidg2 (rel mouse, unused by UI yet)
   -> dwc3 UDC a600000.usb  (peripheral mode, via usb-peripheral DT overlay)
   -> blue USB-A port -> cable -> Target  (target must HOST the port — see gotchas)
```

## Services (all systemd, enabled, auto-start in this order)

| unit | executable | listens / touches | role |
|------|------------|-------------------|------|
| `q6a-kvm-pipeline.service` | `/usr/local/bin/q6a-kvm-configure` | `/dev/video0`, EDID | oneshot: load EDID, lock source, set DV-timings, route + format the camss pipe |
| `q6a-kvm-streamer.service` | `/usr/local/bin/q6a-kvm-stream` | `:8080`, `/dev/video20` | gst feeder (camss→loopback) + ustreamer MJPEG |
| `q6a-kvm-watchdog.service` | `/usr/local/bin/q6a-kvm-watchdog` | — | re-kick streamer when the source changes (sleep/wake, resolution, reboot, stall) |
| `q6a-kvm-gadget.service` | `/usr/local/bin/q6a-kvm-gadget` | `/dev/hidg0..2`, MSD | build + bind the configfs USB composite gadget |
| `q6a-kvm-controlplane.service` | `/usr/local/bin/qkvm` | `:8000` | web UI + single-user auth + HID input bridge + video proxy |

## Ports, devices, state

- **Ports:** `8080` ustreamer (`/stream` MJPEG, `/snapshot` JPEG, `/state` JSON), `8000` qkvm web UI.
- **V4L2:** `/dev/video0` camss RDI capture; `/dev/video20` v4l2loopback; `/dev/video17`/`18` = Venus decoder/encoder (encoder **unstable**, see gotchas).
- **HID gadget:** `/dev/hidg0` keyboard (8-byte), `/dev/hidg1` abs mouse (6-byte), `/dev/hidg2` rel mouse (4-byte). UDC: `/sys/class/udc/a600000.usb`.
- **State files:** `/etc/q6a-kvm/edid.hex` (capped EDID); under `/var/lib/q6a-kvm/`: `auth.json` (0600 creds), `devices.json` (WoL targets), `macros.json`, `msd.img` (64 MB mass-storage backing).

## Control plane (`qkvm`) internals

Go, **zero external deps**, source in `controlplane/`, binary `/usr/local/bin/qkvm`.

- `main.go` — HTTP routes, flags, video reverse-proxy, auth API.
- `auth.go` — single-user auth: PBKDF2-HMAC-SHA256 (600k iters, stdlib), HMAC-signed session cookie (stateless, survives restart via persisted secret).
- `hid.go` — input bridge: `event.code`→HID usage keymap, report assembly, device writers.
- `ws.go` — minimal hand-rolled RFC-6455 WebSocket (read text frames; no dep).
- `web/index.html` — embedded SPA: login/setup gate, then video + input capture.
- **Input protocol** (browser→server WS JSON): `{"t":"k","code","down"}` · `{"t":"m","x","y","buttons"}` (x/y 0..1 of video rect) · `{"t":"w","dy"}` · `{"t":"chord","codes":[...]}` (shortcuts) · `{"t":"type","text"}` (typed via US-layout keymap).
- **Auth:** first visit with no account → setup; then login. Gates `/stream`, `/snapshot`, `/ws/input`, and all `/api/devices*`+`/api/macros*`. Static page public. `/api/{status,setup,login,logout}`.
- **Features API (gated):** `GET/POST /api/devices` + `/api/devices/{wake,delete}` (**Wake-on-LAN** by MAC, `store.go`+`wol.go`); `GET/POST /api/macros` + `/api/macros/{run,delete}` (`macro.go`: steps `text:`/`key:`/`chord:`/`delay:`). UI (**RAD-KVM** branded) sidebar = Shortcuts / Devices / Macros.
- **Flags:** `-addr :8000 -stream http://127.0.0.1:8080 -kbd /dev/hidg0 -mouse /dev/hidg1 -auth-file /var/lib/q6a-kvm/auth.json`.

## Build & deploy

- **qkvm:** cross-compile on a dev machine (no Go needed on the board):
  `cd controlplane && CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o /tmp/qkvm .`
  then `scp /tmp/qkvm <user>@<board>:/tmp/qkvm.new` and run `tmp/board-qkvm-install.sh`
  (stops the service, swaps the binary, restarts — avoids "text file busy").
- **tc358743 module + overlays + pipeline/streamer/gadget/watchdog:** built/deployed by the `tmp/board-*.sh` scripts (regenerated per task; `tmp/` is gitignored). Canonical sources live in `kernel/`, `packaging/`, `gadget/`, `edid/`.

## Gotchas that cost real time (do not relearn)

- **DT overlays are applied by systemd-boot**, via the `devicetree-overlay` line in `/boot/efi/loader/entries/RadxaOS-<kver>.conf`, regenerated from `/boot/dtbo` enabled state by `rsetup` / `yz-update-overlays`. Dropping a `.dtbo` in the ESP `dtbo/` dir does **nothing**. (boots via `\EFI\systemd\systemd-bootaa64.efi`; extlinux.conf is vestigial.)
- **USB cabling:** the board's lone USB-C is **power-only**; the gadget rides the **blue USB-A** port. A USB-A connector signals "host," so a C→A cable to a USB-C-only target won't power/host it (`udc state = not attached`). The **target needs a USB-A host port** — direct, or via a hub / USB-C→USB-A adapter.
- **Capture follows the source only via the watchdog.** The camss pipe configures once; on any source change it freezes until `q6a-kvm-watchdog` re-kicks the streamer (also fixes no-signal-at-boot).
- **Venus HW H.264 is a dead end on this kernel** — the encoder (`/dev/video18`) hard-resets the SoC on the first encode. MJPEG (~6% of one core) stays. Revisit only with a serial console.
- **Connector path:** C790 cannot use the Q6A 4-lane port; real path is C790 15-pin → **CAM2 (2-lane, CSIPHY2, `cci1_i2c0`)**, RPi camera pinout. 2 lanes ⇒ ≤74 Mpix/s (720p60 / 1080p30). 1080p50/60 (most targets' default) is physically impossible on 2 lanes — needs USB-UVC capture or 4-lane hardware.
- **EDID write needs I2C chunking on CCI.** The control I2C is the Qualcomm **CCI**, which rejects long transfers with `-95` (EOPNOTSUPP). Mainline tc358743 writes the 128-byte EDID blocks in one transfer → fails → bridge has **no EDID** → every source reads empty and falls back to 1080p60. Our driver patch (`I2C_BURST`, `kernel/tc358743/`) chunks `i2c_rd`/`i2c_wr` to ≤8 bytes; verify with `v4l2-ctl -d <bridge> --get-edid` (valid `00 ff ff…` = good). **Open item:** EDID *presentation* to the source (HPD/5V) — bridge enables EDID-over-DDC only on source +5V detect; if a source reads empty despite a valid bridge EDID, that's the path to debug.

## Status & roadmap

- **Phase 1** CSI capture ✅ · **Phase 2** MJPEG stream ✅ · **Phase 3** USB HID+MSD gadget ✅
- **Control plane (RAD-KVM)**: live video + keyboard + absolute mouse + single-user auth ✅; sidebar with **Shortcuts** (key combos + type/paste), **Devices** (Wake-on-LAN by MAC), **Macros** (save/run scripted sequences) ✅ (boot-persistent service)
- **Next:** relative-mouse toggle → mass-storage image mounting from the UI → one-line installer (`curl | sudo bash`, ships prebuilt binaries, builds only the tc358743 module on-device) → TLS + safe remote access (VPN/Tailscale or outbound relay; **never** a direct port-forward) → flashable image → full rename to RAD-KVM (repo/services/paths).

Decision on record: build our **own** control plane (not kvmd). Single-user
appliance with a custom UI + features; reuse only the hard primitives (ustreamer
video, `/dev/hidg*` HID); kvmd is reference-only.
