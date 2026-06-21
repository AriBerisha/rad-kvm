# Bring-up log

Brutally honest running log of experiments. Newest entries on top.

---

## 2026-06-21 (pm) — first flash-test: the systemd-boot "no base devicetree" trap

Flashed the slim `.gz` to a Q6A and hit a wall: SSH + mDNS (`rad-kvm.local`) up, but
`:8000` dead. qkvm crash-looped on `open /dev/hidg0: no such file or directory` → the
gadget had no UDC → `/sys/class/udc` empty → `dr_mode=host` on `usb@a600000` → the
usb-peripheral overlay never applied. The **camera was dead too** (no `tc358743`, only
the empty `/dev/video20` loopback) — so **no** overlay was applying.

The multi-hour hunt:
- The board has **both** a `/boot/extlinux/extlinux.conf` (U-Boot) and a **systemd-boot**
  BLS loader entry on the ESP. extlinux had the right `fdtoverlays` line, but **U-Boot
  ignores it** — it boots a fixed/embedded base DTB. Proven: setting `fdt`/`fdtoverlays`
  in extlinux and even overwriting `/usr/lib/.../qcs6490-radxa-dragon-q6a.dtb` left
  `/sys/firmware/fdt` ≈ the plain base (no overlays).
- `/boot/uEnv.txt` is **retired** ("use `rsetup`"). The real boot path is **systemd-boot**,
  whose loader entry had `devicetree-overlay` but **no base `devicetree`** → the overlays
  have nothing to attach to → silently dropped.

Fix that worked (now in `install.sh`): `fdtoverlay`-merge `q6a-tc358743` + `usb-peripheral`
into the base DTB (it has `__symbols__`, so the merge succeeds and the result shows
`usb@a600000 dr_mode=peripheral`), copy it to the ESP, and set the loader entry's base
`devicetree` to it (dropping the overlay line). Reboot → `dr_mode=peripheral` → UDC →
`/dev/hidg0` → qkvm bound `:8000` (HTTP 200), camera node present. Image rebuilt.

Also confirmed on hardware: **`linux-firmware` removal is safe** (boots + networks),
RadxaOS **first-boot grew the rootfs** to fill the card, **mDNS `rad-kvm.local`** works
over Ethernet, and **balenaEtcher can't decompress our `.xz`** (single- *or* multi-block)
so the distributable is **`.gz`**.

---

## 2026-06-21 — packaging: one-line installer + slim flashable image (PiKVM-class)

Turned the working board into something reproducible/shippable.

- **`install.sh`** — one shot on stock RadxaOS: builds the patched tc358743 module,
  v4l2loopback, ustreamer, qkvm from source; installs EDID/scripts/services/configs;
  compiles + enables the camera + usb-peripheral overlays (wiring the systemd-boot
  loader entry); enables all services. One reboot, then self-configures. Writing it
  caught two latent bugs: it was missing **gstreamer** (the streamer uses gst-launch),
  and `scripts/build-tc358743.sh` still curl+patched mainline (so it MISSED the CCI
  I2C fix) — both fixed (build from the vendored source).

- **Slim flashable image** (`image/`) — bake RAD-KVM into the vendor RadxaOS image
  with Docker on Apple Silicon: a privileged **native-arm64** container loop-mounts
  the `.img` + `chroot`s (no qemu), runs `install.sh` in image mode (`RADKVM_KVER`
  targets the IMAGE's kernel; `RADKVM_IMAGE_BUILD` skips the reboot), strips the
  desktop, zeroes free space, `xz`. Result: **~770 MB** vs the **1.5 GB** vendor
  download — and it ships the whole stack, not a bare desktop.

  Real fights: the GNOME rootfs ships **15 MB free**, so `apt purge` couldn't even
  run → `rm` firefox's dir first to bootstrap room, then purge the **dpkg-query'd**
  (not guessed) names — `gnome-shell`/`gdm3`/`gnome-control-center`/`gnome-software`/
  `xserver-xorg-core`/`firefox-esr` + autoremove; remove generic **`linux-firmware`**
  (533 MB — Q6A fw is in `-dragonwing`/`radxa-firmware-qcs6490`, WiFi in `aic8800`)
  for space + size. RadxaOS ships headers in `/usr/src` but no `/lib/modules/$KVER/
  build` symlink → create it. Parse mapped devs from `kpartx -av` (not `-l`, which
  re-loops to a phantom). Pin `depmod` to `$KVER` (bare `depmod -a` hit the build
  container's `6.10-linuxkit`). Loop/dm state self-cleaned per run.

- **Headless discovery**: `avahi-daemon` + hostname **`rad-kvm`** →
  `http://rad-kvm.local:8000`, no IP hunting (pikvm.local model).

**Verified:** the image BUILDS end to end (8/8 stages, module compiles vs the image
kernel, both overlays in the loader entry, all services enabled, 770 MB). **Not yet
verified:** booting it on hardware (can't flash from a build host). The one aggressive
cut is `linux-firmware`; if boot/net misbehaves, rebuild keeping it.

---

## 2026-06-20 — RAD-KVM features + the EDID root cause (CCI can't do big I2C writes)

Product/UI name is now **RAD-KVM** (binary/services/paths stay `qkvm`/`q6a-kvm`
to avoid breaking the deploy; full rename is a later pass). Added a real feature
set on top of the auth'd control plane, all committed + smoke-tested:

- **Wake-on-LAN** (`controlplane/store.go`+`wol.go`): saved-target store + magic
  packet (SO_BROADCAST via the dialer Control hook). REST `/api/devices[/wake|/delete]`.
- **Shortcuts**: one-click key combos → `{"t":"chord",codes:[…]}` over the input WS.
- **Type/paste**: `{"t":"type",text}` types a string via a US-layout char→HID map.
- **Macros** (`macro.go`): stored scripts (`text:`/`key:`/`chord:`/`delay:`), run
  server-side on the HID. `/api/macros[/run|/delete]`.
- **UI rework**: top bar + video + sidebar (Shortcuts / Devices / Macros), toast,
  a `/api/source` overlay that explains "no video" (e.g. source too high-res).
- **Watchdog backoff**: it now reads the source pixelclock and *stops re-kicking*
  on an uncarryable mode (was thrashing every ~14s on a 1080p60 source).

**The big one — why no source's resolution ever "took".** Chasing why a second
Q6A source showed no video, the bridge was locked at **1080p60** (pixelclock
138.65 MHz) — which the **2-lane CAM2 link physically cannot carry** (~1.1 Gbps/
lane needed > the TC358743's ~1 Gbps/lane). The 2-lane hard ceiling is
**720p60 / 1080p30** (~74 Mpix/s); 1080p50/60 need a USB-UVC capture path or
4-lane hardware, not software. We broadened the EDID (`rad-kvm.hex`: 640×480,
**800×480**, 800×600, **1024×600**, 1024×768, 1280×720 pref, 1080p30) to steer
EDID-honoring sources to carryable modes — but sources kept reading **EMPTY EDID**
and falling back to their defaults.

Root cause, found on the source side (`/sys/class/drm/<conn>/edid` empty) then
confirmed on the bridge: the control I2C is the Qualcomm **CCI**, which has a small
max transfer length and **rejects long transfers with `-95` (EOPNOTSUPP)**.
Mainline `tc358743` writes the EDID in **128-byte blocks in one transfer**, so:

```
tc358743 18-000f: i2c_wr: writing register 0x8c00 ... failed: -95
```

→ the bridge's EDID RAM is never programmed → every source reads an empty EDID.
**Fix:** patched `i2c_rd`/`i2c_wr` in our vendored driver to split every transfer
into ≤8-byte bursts (`I2C_BURST`); the chip auto-increments the register address,
so it's transparent. Built from the vendored source (`tmp/board-build-tc358743-v2.sh`,
scp the `.c`+`.h`), rebooted. **Verified:** `v4l2-ctl --get-edid` now returns a
valid EDID (`00 ff ff ff ff ff ff 00` … "RAD-KVM" … 800×480 DTD), **zero `-95`**.
This is the third tc358743 patch — see `kernel/tc358743/README.md`.

**Open loose end:** even with the EDID now in the bridge, the source still read
EMPTY over HDMI — the TC358743 only enables EDID-over-DDC + asserts HPD when it
detects the source's **+5V** (`if (tx_5v_power_present) tc358743_enable_edid`),
on a delayed work. Need to confirm 5V detect + HPD assertion (the bridge→source
EDID *presentation* path), separate from the now-fixed EDID *write*. The source
was observed on 800×480 (carryable) at last check; capture-confirm pending.

---

## 2026-06-19 — CHECKPOINT: browser control plane (our own) + auth + watchdog

The KVM is now usable from a browser, end to end, behind a login. **Decision:
build our own control plane, not kvmd** — single-user appliance with a custom UI
+ features, so kvmd (whose value is its own UI/auth) would be a fight; we reuse
only the hard primitives (ustreamer video, `/dev/hidg*` HID).

- **`qkvm`** (Go, zero deps, `controlplane/`): serves the web UI, reverse-proxies
  the MJPEG video on one origin, and bridges browser keyboard + **absolute mouse**
  over a **hand-rolled WebSocket** to `/dev/hidg0`/`hidg1`. Proven: cursor + typing
  drive the target. Runs as `q6a-kvm-controlplane.service`.
- **Auth:** first-run account setup → login; PBKDF2-HMAC-SHA256 (600k, stdlib) +
  HMAC-signed session cookie (stateless, survives restart). Gates `/stream`,
  `/snapshot`, `/ws/input`; static page public. 10/10 endpoint checks pass
  (401 unauth, double-setup blocked, tampered cookie rejected, 0600 store).
- **Capture watchdog** (`q6a-kvm-watchdog`): the camss pipe configures once and
  froze whenever the source changed; the watchdog polls bridge DV-timings +
  ustreamer fps and re-kicks the streamer on resolution change / sleep-wake /
  reboot / no-signal-at-boot / stall. Verified auto-recovering.
- **Deploy model:** `qkvm` is cross-compiled (`CGO_ENABLED=0 GOOS=linux GOARCH=arm64`, static, ~6 MB) on a workstation and `scp`'d to `/tmp/qkvm.new`; `tmp/board-qkvm-install.sh` stops the
  service, swaps the binary, restarts (avoids "text file busy" on a running bin).

Testing gotcha worth remembering: control the KVM from a **second device**, not
the target itself (feedback loop). On a **multi-monitor** target, the board
captures one specific display (the EDID-named "q6a-kvm" 1280×720 one) and absolute
mouse maps into the primary display's space — confusing; a single-screen target is
the clean case. A "frozen" feed was twice explained by the captured display being
idle/static (modern Macs drop HDMI refresh when a display has no motion) — not a
bug. Full current-state map now in `docs/architecture.md`.

---

## 2026-06-19 — PHASE-3: USB gadget (HID + MSD) live, + two hard-won lessons

**Phase 3 done.** The board now presents a PiKVM-class USB composite gadget to
the target and drives it: HID keyboard (`/dev/hidg0`), HID mouse absolute
(`/dev/hidg1`) and relative (`/dev/hidg2`), plus a 64 MB removable mass-storage
LUN — one configfs/libcomposite gadget (`gadget/q6a-kvm-gadget.sh`; report
descriptors match kvmd). Verified end-to-end: the Mac enumerates "q6a-kvm
Composite KVM" and the cursor moves when we write to `/dev/hidg2`.
Boot-persistent via `gadget/q6a-kvm-gadget.service`. No kernel work — the whole
gadget stack was already compiled in the vendor kernel; only configfs + the OTG
overlay were missing.

Getting the dwc3 into peripheral mode and the cabling right took the most time:

1. **OTG → peripheral via the vendor `usb-peripheral` overlay** (`&usb_1` /
   `usb@a600000` → `dr_mode=peripheral`) yields the UDC `a600000.usb`. Wi-Fi
   (aic8800) and the local keyboard live on the *other* controller
   (`8c00000`/usb_2), so flipping `usb_1` costs neither SSH nor the rescue kbd —
   confirmed before rebooting.
2. **Overlay-apply mechanism — the earlier note (item 2 below) was WRONG.** This
   board boots via **systemd-boot** (`efibootmgr` → `\EFI\systemd\systemd-bootaa64.efi`),
   NOT U-Boot/extlinux. The real gate is the `devicetree-overlay` line in
   `/boot/efi/loader/entries/RadxaOS-<kver>.conf`, regenerated from `/boot/dtbo`
   enabled state by `rsetup` / the `yz-update-overlays` postinst hook. Renaming
   the file in the ESP `dtbo/` dir does nothing on its own — that burned several
   reboots. Correct path: enable in `/boot/dtbo`, then
   `DEB_MAINT_PARAMS=configure yz-update-overlays …` (+ append to the loader
   entry if needed), verify `grep devicetree-overlay /boot/efi/loader/entries/*`,
   reboot. Offline sanity check of an overlay: `fdtoverlay -i base.dtb -o m.dtb X.dtbo`.
3. **Connector reality: the single USB-C is power-only.** DT shows every USB
   connector is a `usb-a-connector`; there is no Type-C data port / role switch.
   The gadget can only leave via the **blue USB-A** port (`a600000`). A USB-A
   connector signals "I'm the host," so a C→A cable to a USB-C-only Mac never
   makes the Mac source VBUS (`udc state = not attached`). Fix: put a **USB-A
   host** on the target side (a hub, or a USB-C→USB-A adapter) and run A-to-A to
   the blue port — the hub port supplies VBUS and `udc state → configured`.
   **Hardware requirement for this build: the target needs a USB-A host port (or
   a hub/adapter in between).**

**Venus HW H.264 = dead end on this kernel (deferred).** Detoured to try the
"efficient encoder": `/dev/video18` is the Venus encoder (NV12→H264; gst
`v4l2h264enc` present), but the *first* encode attempt — even from a synthetic
`videotestsrc`, no camera involved — **hard-hung the SoC into a watchdog
reboot**. No ramoops backend and no oops in the journal → uncaptured hard hang
(needs a serial console to debug). The rootfs survived intact each time. Grounded
the decision by measuring first: the CPU MJPEG stream is only **~6% of one core**
(8-core SoC ~98% idle), so there's no CPU pressure to justify the risk. Decision:
**stay on MJPEG**; revisit HW encode later with serial-console capture +
conservative `v4l2h264enc` controls / NV12 alignment / alternate `venus-*.mbn`.

Next: wire `/dev/hidg*` + the MJPEG stream into **kvmd** (browser keyboard/mouse
and MSD mounting), then package as a kvmd platform.

---

## 2026-06-19 — PHASE-1 GATE PASSED: UYVY frame off the VFE RDI

End-to-end capture works. Chain proven:
`TC358743 (HDMI in) → CCI i2c (18-000f) → CSIPHY2 → CSID0 → VFE0 RDI0 →
/dev/video0`, **UYVY 4:2:2**. Source = Mac (extended display) @ **1280x720p50**,
2-lane CAM2. Capture: `v4l2-ctl -d /dev/video0 --stream-mmap --stream-count=30`
→ **55,296,000 bytes = 30 × (1280×720×2)**, remainder 0. The QCS6490 RDI
raw-dump carries UYVY — go/no-go answered YES.

Four distinct bugs had to fall, in order:

1. **Overlay endpoint missing `link-frequencies`.** tc358743 `probe_of` rejects
   the CSI-2 endpoint with `-EINVAL` ("missing CSI-2 properties in endpoint")
   unless `nr_of_link_frequencies != 0`. Added
   `link-frequencies = /bits/ 64 <297000000>` (594 Mbps/lane ×2 = 1188 Mbps;
   fine for ≤1080p30/720p) to `kernel/dts/qcs6490-q6a-tc358743.dtso`. Also
   dropped a vestigial gpio.h include so it builds with plain `dtc`. Reconciled
   the stale `tmp/` copy (it, not kernel/dts, was the actual build source — a
   trap; now identical).
2. **Deploy went to the wrong store.** U-Boot applies overlays from the **ESP**
   `/boot/efi/RadxaOS/<kver>/dtbo/` (per `managed.list`), NOT rootfs
   `/boot/dtbo/`. Editing `/boot/dtbo` alone = byte-identical dmesg across
   reboots. **This corrects the earlier "active store = /boot/dtbo" note below.**
   Fix: copy the dtbo into the ESP dir too, then reboot. After that the bridge
   probed: `tc358743 18-000f: tc358743 found @ 0x1e (Qualcomm-CCI)`.
3. **No EDID → no signal.** Empty `edid/`; an HDMI source won't transmit without
   a sink EDID. Created `edid/tc358743-1080p.hex` (256 B, checksums fixed),
   loaded via `v4l2-ctl --set-edid --fix-edid-checksums`. Mac then locked
   720p50; `--set-dv-bt-timings query` applied it; set bridge source pad to
   `UYVY8_1X16` → `Color space: YCbCr 422`, `Lanes in use: 2` (overlay's 2 lanes
   confirmed carrying YUV).
4. **camss couldn't get the link freq (two TC358743 driver gaps).** STREAMON
   `-EINVAL` + `call_s_stream` WARN + `qcom-camss: Cannot get CSI2 transmitter's
   link frequency`. Root causes, both in mainline tc358743: (a) it exposes **no
   `V4L2_CID_LINK_FREQ`** control; (b) it declares `MEDIA_ENT_F_VID_IF_BRIDGE`,
   but `camss_find_sensor_pad()` only stops at `MEDIA_ENT_F_CAM_SENSOR`, so camss
   never even reaches the control. Vendored + patched the driver in
   `kernel/tc358743/` (source from mainline v6.18): added a read-only
   `V4L2_CID_LINK_FREQ` int-menu fed by the DT link-frequency (297 MHz = the
   bridge's actual fixed CSI line rate), and switched the entity function to
   `MEDIA_ENT_F_CAM_SENSOR`. Verified `camss_get_pixel_clock` failures are
   non-fatal (`if (ret) pixel_clock[i]=0`) so no `V4L2_CID_PIXEL_RATE` needed.
   Rebuild via `tmp/board-build-tc358743.sh` (curls v6.18, applies all 5 hunks,
   `make -C $KDIR M=$PWD`, modules_install, depmod). Live `rmmod` is refused
   while camss is bound → **reboot to swap the module**.

Repro from cold: `tmp/board-go.sh` (EDID → lock → timings → UYVY → route pipe →
capture) does the whole runtime dance; resolution auto-detected. Note: pipeline
config (links/formats/EDID) is all runtime and lost on reboot — needs a
persistence service (next).

Open follow-ups: persist the pipeline + EDID at boot (systemd/udev); stand up
`ustreamer` on `/dev/video0`; try 1080p (raise DT link-freq to 486 MHz / 972
Mbps for 1080p50 UYVY over 2 lanes, per the driver's own comment); USB gadget
(HID + MSD) for the control path.

### Boot persistence — DONE (same day)
`packaging/q6a-kvm-configure.sh` + `q6a-kvm-pipeline.service` (systemd oneshot,
RemainAfterExit), installed via `tmp/board-deploy-persistence.sh`. After a cold
boot the journal shows `EDID loaded` → `pipeline ready: 1280x720 UYVY on
/dev/video0`, and `/dev/video0` is UYVY/1280x720 with no manual step. The
configure script polls ≤30 s for the bridge subdev + `/dev/media0`, loads the
EDID, waits ≤8 s for a lock, auto-detects resolution, then routes + formats the
pipe. Configures **once at boot** (no live resolution-change handling yet —
deferred to the ustreamer step). `media-ctl` now reports the bridge as
`subtype Sensor`, confirming the CAM_SENSOR entity patch is live.

### MJPEG stream live + autostart (Phase 2) — DONE (same day)
Browser shows live video at `http://<board>:8080`. Path:
`/dev/video0 (camss, MPLANE) → gstreamer → /dev/video20 (v4l2loopback) →
ustreamer → HTTP`. Hurdles:
- **ustreamer can't open the multiplanar camss node** ("Video capture is not
  supported"). Bridge via **v4l2loopback** (single-planar) fed by a gst pipe
  (`v4l2src ! video/x-raw,format=UYVY,WxH ! v4l2sink`); gst handles mplane,
  ustreamer reads the loopback.
- **v4l2loopback apt 0.12.7 won't build on 6.18** (`v4l2_fh_del` got a 2nd arg).
  Built **v0.15.3** from source (it's `#if`-guarded for 6.18).
- **ustreamer apt 5.4 aborts on the loopback** ("Got unexpected writing event" —
  it watched the fd for write-readiness; v4l2loopback always reports writable).
  Built **v6.60** from source (the check was removed upstream).
- **"buffer already used" spam**: the loopback yields 2 buffers (producer pins
  the count) and the rule is **buffers ≥ workers+1**, so `--workers=1` fixed it.
- **Resolution drift / 640x480 safe-mode** was a *wrong cable* (a monitor — a
  sink — on the C790 input). With the Mac correctly feeding it at 720p it's
  stable. Note the 2-lane budget ≈ 74 Mpix/s (1080p30 / 720p60 max); 1080p60
  overflows — fixed durably by the capped EDID (below).

### Capped EDID — DONE (same day)
`edid/tc358743-720p.hex` (generator `edid/make-edid.py`) advertises preferred
720p60 + 720p50 / 1080p30 / 1080p25 and nothing heavier, so the source can't
drift to 1080p50/60 or a 640x480 safe mode. Installed to `/etc/q6a-kvm/edid.hex`
(now the persistence default). On deploy the Mac immediately re-locked to 720p
(it picked 50 Hz from the set) and the stream continued. Hand-built blob,
validated by self-decode (every advertised mode ≤ 74.25 Mpix/s; both checksums 0).

Packaged in `packaging/`: `q6a-kvm-stream.sh` wrapper +
`q6a-kvm-streamer.service` (After=pipeline, Restart=always, auto-detects res),
plus `v4l2loopback` modprobe.d (8 buffers) + modules-load.d. Survives reboot:
loopback loads → pipeline configures camss → streamer feeds + serves, hands-free.

TODO: HW H.264 via Venus later; then USB HID/MSD gadget (Phase 3).

---

## 2026-06-17 — First boot on hardware (recon)

Board booted from the official Radxa image (Ubuntu 24.04 GNOME) on microSD.
Access: `root@radxa-dragon-q6a` (radxa/radxa). Commands run manually by the
operator (SSH key push from the dev Mac wasn't set up; manual command flow for
now). A separate SPI/GPIO HDMI mini-screen is attached to the 40-pin header —
unrelated to the CSI path.

Recon results:
- `uname -r` = **6.18.2-3-qcom** (matches the offline image audit).
- `lsmod`: **venus_enc/dec/core loaded** (HW codec live); **camss NOT loaded** —
  expected, it's `=m` and only probes once a camera DT node exists. No overlay
  enabled yet ⇒ no `/dev/media0` (`media-ctl` => ENOENT -2), and `/dev/video0,1`
  are the **Venus m2m codec**, not capture.
- `/sys/class/udc`: **empty** ⇒ OTG still host mode (usb-peripheral overlay not
  applied). Expected.
- Toolchain: **gcc 13.3.0**, `/lib/modules/6.18.2-3-qcom/build` present ⇒ can
  build the tc358743 module out-of-tree.
- Kernel config (from extracted config) confirms build deps present and no
  blockers: `CONFIG_HDMI=y`, `CONFIG_CEC_CORE=y`, `CONFIG_V4L2_FWNODE=m`,
  `CONFIG_V4L2_ASYNC=m`, `CONFIG_REGMAP_I2C=y`; and **no** MODULE_SIG_FORCE,
  **no** SECURITY_LOCKDOWN_LSM, **no** TRIM_UNUSED_KSYMS ⇒ unsigned OOT module
  will build + insmod fine.

Next: build `tc358743.ko` (mainline v6.18 source vs the shipped headers) →
modules_install + depmod → apply CAM2 overlay (loads camss, creates media graph)
→ reconnect C790 → i2cdetect 0x0f on cci1 → EDID → land a UYVY frame.

### tc358743 module: BUILT + LOADS (same day)
- `tc358743.c` + `tc358743_regs.h` from **mainline v6.18** (raw github), built
  out-of-tree against `/lib/modules/6.18.2-3-qcom/build`. One-line Makefile
  (`obj-m := tc358743.o`); built via `make -C $KDIR M=$PWD modules`.
- Only a benign "compiler differs" warning (kernel built w/ gcc 12.2, we used
  13.3) — harmless (gcc version isn't in vermagic; no MODVERSIONS/sig).
- `modules_install` + `depmod -a` + `modprobe tc358743` => loads clean, pulls
  v4l2_dv_timings/v4l2_fwnode/v4l2_async/videodev/mc. Idle until a DT node exists.

### CAM2 overlay: COMPILED + fixups verified
- `tmp/q6a-tc358743.dtso` (lean, no includes) -> `dtc -@ -I dts -O dtb` ->
  `q6a-tc358743.dtbo`. `__fixups__` correctly references camss, cci1, cci1_i2c0,
  vreg_l10c_0p88, vreg_l6b_1p2 — so it binds into the base DT.

### Overlay apply mechanism on this image (extlinux/u-boot path)
- Compiled package overlays: `/usr/lib/linux-image-<kver>/qcom/overlays/*.dtbo`
  (the source `rsetup rebuild_overlays <kver>` regenerates from).
- Active store: `/boot/dtbo/*.dtbo[.disabled]` + `/boot/dtbo/managed.list`
  (`.disabled` suffix = off). EFI mirror at `/boot/efi/RadxaOS/<kver>/dtbo/`.
- `extlinux.conf` is auto-generated by `u-boot-update` (reads
  `/etc/default/u-boot`: `U_BOOT_FDT_OVERLAYS`, dir `/boot/dtbo/`). No `fdt`
  line currently (base FDT comes from firmware; overlays apply on top).
- Plan: drop our .dtbo into the package overlay dir, `rsetup rebuild_overlays`,
  enable via `rsetup` TUI (category "camera"), reboot with C790 on CAM2.
- Recovery if a bad overlay blocks boot: pop SD in the Mac, delete the .dtbo
  from /boot/dtbo (or revert extlinux.conf).

---

## 2026-06-17 — Connector compatibility (the handover's central wiring assumption is WRONG)

Investigated how the C790 physically attaches, using Radxa accessory docs +
schematic v1.21 (downloaded, text-extracted with `pdftotext`).

**Finding: the C790's 22-pin / 4-lane output does NOT fit the Q6A 4-lane port.**
- Q6A **CAM1 (4-lane) = 31-pin 0.3 mm** FPC (`CAM_31P`, schematic p.32), with
  5V pins. The IMX577/4K cameras use this (31-pin 0.3 mm).
- Q6A **CAM2/CAM3 (2-lane) = 15-pin 1.0 mm** FPC. The 8M-219 (IMX219) uses this.
- C790 has **two** outputs: front **15-pin 1.0 mm** (Pi 4/3, 2-lane) and back
  **22-pin 0.5 mm** (Pi 5/CM4, 4-lane). None match the 31-pin.

**Viable path = C790 FRONT 15-pin → Q6A CAM2 or CAM3 (2-lane).** Confirmed from
schematic p.32 that CAM2 (J16) is wired to the **Raspberry Pi camera pinout**
(GND/data/clock interleave; pin11 reset; pin12 MCLK = NC; pin13 SCL; pin14 SDA;
pin15 3V3 — no 5V). The C790's front connector is that same Pi pinout, so a
plain 15-pin 1.0 mm FPC mates them and the C790 powers off connector 3V3. See
docs/hardware.md for the full pin table + the orientation/continuity safety
check (a wrong cable flip crosses 3V3↔GND).

Trade-off: **2 lanes => ~1080p25-30 / 720p ceiling.** Fine for KVM; matches the
C790's "1080p25" marketing. 4-lane 1080p60 is off the table on this hardware.

**Overlay reworked** accordingly: `kernel/dts/qcs6490-q6a-tc358743.dtso` now
targets **CAM2 = CSIPHY2** (`port@2`, `data-lanes=<0 1>`) with control I2C on
**`&cci1_i2c0`** (note: cci1, not cci0), modelled on the vendor
`cam2-radxa-camera-8m-219.dtso`. CAM3 variant = port@3 / `cci1_i2c1`.

Open hardware questions for the bench: exact C790 crystal freq (assumed 27 MHz);
TC358743 strap address (0x0f vs 0x0e); which 15-pin FPC orientation is correct.

---

## 2026-06-17 — Image inspection (offline, from macOS; board not yet booted)

Inspected the vendor image **before flashing** by loop-mounting it on the Mac.
This answered most of the handover's "unknowns" without touching hardware.

**Image:** `radxa-dragon-q6a_noble_gnome_r2.output_512.img` (6 GB, Ubuntu 24.04.4
GNOME). GPT layout:
- p1 @16 MiB (16 MiB) — Linux fs (misc/config)
- p2 @32 MiB (1 GiB) — EFI System Partition (FAT) = `/boot` (systemd-boot)
- p3 @1056 MiB (~4.5 GiB) — ext4 rootfs

How (repeatable):
```bash
hdiutil attach -imagekey diskimage-class=CRawDiskImage -nomount <img>   # -> /dev/disk5
mount -t msdos /dev/disk5s2 /tmp/q6a_boot                                # FAT /boot
# rootfs is ext4 (not mountable natively on macOS); read it without mounting:
brew install e2fsprogs
DEBUGFS=$(brew --prefix e2fsprogs)/sbin/debugfs
$DEBUGFS -R "dump /boot/config-6.18.2-3-qcom <out>" /dev/disk5s3
```

### Kernel
- **6.18.2-3-qcom** — newer than the handover's "~6.16" guess. Boot via
  systemd-boot, entry `RadxaOS-6.18.2-3-qcom.conf`, title "Ubuntu 24.04.4 LTS".
- Console: `console=ttyMSM0,115200n8 ... console=tty1`. root by UUID.

### Driver audit (from `/boot/config-6.18.2-3-qcom`)
**The single gap is the TC358743 bridge driver.** Everything else is present.

| Capability | Symbol | State |
|---|---|---|
| **TC358743 HDMI->CSI bridge** | `CONFIG_VIDEO_TC358743` | **ABSENT** ← the only gap |
| Qualcomm camss (capture) | `CONFIG_VIDEO_QCOM_CAMSS` | `=m` ✓ |
| Venus HW encoder | `CONFIG_VIDEO_QCOM_VENUS` | `=m` ✓ |
| Iris HW encoder | `CONFIG_VIDEO_QCOM_IRIS` | `=m` ✓ |
| V4L2 core / media ctrl | `CONFIG_VIDEO_DEV` / `CONFIG_MEDIA_CONTROLLER` | `=m` / `=y` ✓ |
| USB DWC3 | `CONFIG_USB_DWC3` | `=y` ✓ |
| DWC3 Qualcomm glue | `CONFIG_USB_DWC3_QCOM` | `=m` ✓ |
| DWC3 dual-role | `CONFIG_USB_DWC3_DUAL_ROLE` | `=y` ✓ |
| Gadget framework | `CONFIG_USB_GADGET` / `LIBCOMPOSITE` / `CONFIGFS` | `=y`/`=m`/`=m` ✓ |
| HID gadget | `CONFIG_USB_CONFIGFS_F_HID` / `USB_F_HID` | `=y` / `=m` ✓ |
| Mass-storage gadget | `CONFIG_USB_CONFIGFS_MASS_STORAGE` / `USB_F_MASS_STORAGE` | `=y` / `=m` ✓ |
| FunctionFS | `CONFIG_USB_FUNCTIONFS` | `=m` ✓ |

Shipped media i2c modules: `imx214 imx219 imx412 imx415 ov5640 ov5645 ccs-pll
ir-kbd-i2c`. No `tc358743`, no `adv748x`.

**Consequence:** the big architectural risks the handover flagged (does camss
exist? is there a gadget stack? is the OTG controller dual-role? is there a HW
encoder?) are **all resolved positively from the image alone.** The remaining
build task is one driver, and the remaining true unknown is a runtime question
(does camss RDI carry UYVY on this SoC — see "Still unknown" below).

### Kernel build path is easy
- `linux-headers-6.18.2-3-qcom` is installed (`/usr/src/...`), and
  `/lib/modules/6.18.2-3-qcom/build` symlinks to it.
- So TC358743 can be built **out-of-tree as a module** against the shipped
  headers — **no full kernel rebuild.** Plan: take `tc358743.c` +
  `tc358743_regs.h` from the matching 6.18 source, add a one-line Makefile,
  `make -C /lib/modules/$(uname -r)/build M=$PWD modules`. Watch for deps it
  selects in-tree (HDMI helper lib `CONFIG_HDMI`, optional CEC); confirm those
  symbols at build time.
- Fallback if out-of-tree linking fails: rebuild the Radxa kernel with
  `CONFIG_VIDEO_TC358743=m`.

### Device tree — Radxa already ships the relevant overlays
Overlays live on `/boot` and in source at `/usr/src/radxa-overlays-0.2.18`
(a DKMS package). Activation is via **`rsetup`** — `uEnv.txt` and
`hw_intfc.conf` are **retired** (their stub contents say so). Kernel cmdline
edits go through `/etc/kernel/cmdline` + `u-boot-update`.

Decoded the useful ones:
- **`...-usb-peripheral.dtbo`** — sets `&usb_1 { dr_mode = "peripheral"; }`.
  This is Phase 3's OTG prerequisite, ready as a toggle. (`&usb_1` = the OTG
  controller label.)
- **`...-cam1-imx577.dtso`** — the gold template. Proves CAM1 wiring:
  - **CAM1 == CSIPHY0 == the 4-lane connector** (`clock-lanes=<7>`,
    `data-lanes=<0 1 2 3>` on the camss side; `<1 2 3 4>` on the sensor side).
  - **Camera control I2C is Qualcomm CCI (`&cci0_i2c0`), NOT a 40-pin-header
    I2C bus.** Big correction to the handover's DT sketch: the TC358743 node
    belongs on `cci0_i2c0` at `0x0f`, because the C790's I2C control rides the
    CSI FPC into the CAM1 connector.
  - camss needs `vdda-phy-supply = <&vreg_l10c_0p88>` and
    `vdda-pll-supply = <&vreg_l6b_1p2>`.
  - CMA bumped to 128 MB (`size = <0x0 0x8000000>`) for capture buffers.
- **`...-i2c2.dtso`** — generic bus-enable pattern (pin 28 SCL / 27 SDA),
  `exclusive` with uart2/spi2. Reference only; not our control bus.
- **`...-kvm.dtbo`** — RED HERRING. This is **KVM = Kernel-based Virtual
  Machine (CPU hypervisor enable)**, *not* KVM-over-IP. Naming collision with
  our project. Do not enable expecting capture wiring.

First-draft TC358743 overlay written from the imx577 template:
`kernel/dts/qcs6490-q6a-tc358743.dtso` (untested; verify address/refclk/lanes).

### Still unknown (needs the booted board)
1. **Does QCS6490 camss expose a VFE RDI raw-dump path that carries UYVY?**
   This is the original go/no-go gate and is a *runtime* property — the driver
   being compiled doesn't prove the format/RDI capability on this camss
   revision. Test once a `/dev/videoN` exists.
2. Exact `cci0_i2c0` Linux bus number for `i2cdetect`.
3. Whether the C790's INT/reset lines reach any usable GPIO via the FPC.
4. TC358743 strap address on the actual C790 (0x0f vs 0x0e).

### Next physical step
Flash the image to microSD, boot, get a shell/SSH, then run on-board recon:
`uname -a`, `media-ctl -p`, `ls /dev/video*`, `ls /sys/class/udc`,
`gcc --version && ls /lib/modules/$(uname -r)/build`, `rsetup` overlay list.
