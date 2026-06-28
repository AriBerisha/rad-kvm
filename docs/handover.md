# Project Handover: KVM-over-IP on Radxa Dragon Q6A (TC358743 / C790 CSI capture)

**Working title:** `q6a-kvm`
**Goal:** A self-hosted, open-source KVM-over-IP appliance running on the Radxa Dragon Q6A, capturing the target machine's HDMI through a Toshiba **TC358743** bridge (Geekworm **C790** board) over **MIPI CSI-2**, and emulating keyboard/mouse/mass-storage to the target over **USB OTG** — i.e. a PiKVM-class device on Qualcomm hardware.

This document is the founding spec. It records *why the board was chosen*, *what the data path looks like*, *where the risk is*, and *a concrete phased plan* so a new contributor can pick it up cold.

---

## 1. Why the Dragon Q6A is the right target

The Q6A has the two things a KVM-over-IP build fundamentally needs, which most cheap SBCs lack together:

- **A real MIPI CSI-2 receiver input** — it carries one 4-lane and two 2-lane CSI connectors. The C790's 22-pin 4-lane output maps cleanly onto the 4-lane connector.
- **A dedicated USB OTG/peripheral port** — the board exposes two USB 2.0 ports on the underside, one wired as host and one as a *down/OTG* port. That OTG port is what lets the Q6A pretend to be a USB keyboard/mouse/drive to the target.

SoC is the **Qualcomm QCS6490** (1×A78@2.7 + 3×A78@2.4 + 4×A55@1.9, Adreno 643, hardware video encode). Plenty of headroom for H.264/MJPEG encode of a 1080p stream. Vendor images run a near-mainline kernel (reported ~6.16 with light Radxa patches), which is a big deal for this project because both drivers we depend on live in mainline.

> **Contrast / why not the cheaper option:** the Orange Pi Zero 2W (the board originally considered) has *no* CSI input at all, so the C790 cannot physically attach. The Q6A solves that. Keep this in the README so people don't repeat the dead end.

---

## 2. The two mainline drivers this whole project leans on

1. **`tc358743`** — `drivers/media/i2c/tc358743.c`. A V4L2 sub-device driver for the bridge. It handles HDMI hot-plug, EDID, format detection, and presents the incoming HDMI as a CSI-2 source (typically **UYVY**, YUV 4:2:2). This is the same driver the Raspberry Pi ecosystem uses, so its behaviour is well documented.

2. **`qcom-camss`** — `drivers/media/platform/qcom/camss`. The Qualcomm Camera Subsystem driver. The critical capability: it supports the **VFE RDI path** doing a *raw dump of input data to memory*, and it lists **packed YUV 4:2:2 (UYVY/YUYV/...)** among supported formats. The RDI/raw-dump path skips the Bayer ISP pipeline, which is exactly right for an HDMI bridge — we are not debayering a sensor, we're shovelling already-formed YUV frames to RAM.

**Implication:** the hard architectural question ("can a Qualcomm ISP even ingest a non-camera YUV source?") has a "yes, in principle" answer in the driver docs. The remaining work is *bring-up and wiring*, not writing a capture engine from scratch.

**The honest unknown:** the camss documentation historically describes older SoCs (MSM8916/8996). QCS6490 camss support is comparatively recent. **The first milestone exists specifically to confirm the RDI raw-dump + UYVY path is functional on the QCS6490's camss revision in your chosen kernel.** Treat this as the project's go/no-go gate.

---

## 3. Data path (target diagram)

```
[Target PC HDMI out]
        │  HDMI
        ▼
[C790 / TC358743 bridge] ──I2C (control: EDID, format, lanes)──┐
        │  MIPI CSI-2 (4 lanes, UYVY 4:2:2)                     │
        ▼                                                       │
[Q6A CSIPHY → CSID → VFE RDI raw-dump] ◄────────────────────────┘
        │  /dev/videoN (V4L2, UYVY)
        ▼
[µStreamer]  → MJPEG/H.264 encode → HTTP/WebRTC
        │
        ▼
[kvmd web UI]  ◄── browser (operator)
        ▲
        │  keyboard / mouse / mass-storage events
        ▼
[USB gadget: HID + mass-storage via configfs/functionfs]
        │  USB 2.0 (Q6A OTG port)
        ▼
[Target PC USB in]
```

Audio (TC358743 also emits I2S) and ATX power control (GPIO + optocouplers) are stretch goals — see §7.

---

## 4. Bill of materials

| Item | Notes |
|---|---|
| Radxa Dragon Q6A | 4GB+ is plenty; storage via microSD or eMMC/UFS module |
| Geekworm C790 (TC358743) | Use the **22-pin / 0.5mm / 4-lane** rear connector → Q6A 4-lane CSI |
| FPC cable, 22-pin 0.5mm pitch | Match length/pinout to the Q6A CSI connector; verify lane order |
| USB-A ↔ USB-A cable | Q6A OTG port → target. **Cut/leave-open the +5V (VBUS) line** to avoid back-power and OTG drop-outs |
| 12V PSU (≥18W, ≥24W if loading USB3/PCIe) | Q6A wants 12V via barrel/USB-C PD, *not* 5V |
| HDMI cable | Target → C790 input |
| (Optional) USB HDMI capture dongle (UVC) | For the de-risking MVP in §6, Phase 0 |
| (Optional) optocouplers (e.g. TLP241/G3VM-61A1) | For ATX power/reset control |

---

## 5. Software stack decision

Don't rebuild the KVM app. Reuse the PiKVM userspace and port the *platform glue*:

- **`ustreamer`** — V4L2 capture + JPEG/H.264 streamer. Reads `/dev/videoN`. SoC-agnostic; should run as-is once capture works.
- **`kvmd`** — the PiKVM daemon (web UI, auth, HID, mass-storage, ATX, API). This is the part with Pi/Arch assumptions (udev rules, platform package, gadget scripts).

**Strongest prior art for the port:** `kvmd-armbian` (xe5700 / markuspm forks) already runs kvmd on non-Pi Armbian/Debian boards (Allwinner/Amlogic/Rockchip TV boxes). Their requirements list is essentially our checklist: a board with working **USB OTG (peripheral) mode**, a device tree with `dr_mode` switched from `host` to `peripheral` on the OTG port, and a V4L2 capture source. Study their install script and gadget setup before writing your own.

Also worth a look: **One-KVM** (another community PiKVM fork targeting diverse hardware). And the official PiKVM project explicitly welcomes unofficial ports — they keep a `#unofficial_ports` Discord channel and say they accept patches. Engage there early; you are not the first to port kvmd.

**OS base:** a Radxa-supported Debian/Ubuntu/Armbian image with the near-mainline kernel. You will be rebuilding the kernel and device tree, so pick the image whose kernel source/branch Radxa actually publishes.

---

## 6. Phased roadmap

### Phase 0 — De-risk the *software* before the *capture* (recommended, ~days)
Even though the end goal is CSI/TC358743, prove the whole HID + streaming + web stack first using a cheap **USB HDMI capture dongle** (UVC → `/dev/video0` for free, no kernel work). Get a *fully working KVM over a UVC dongle*. This validates kvmd, ustreamer, the USB gadget, networking, and the web UI independently of the scary CSI bring-up. If Phase 1 stalls, you still have a working product.

### Phase 1 — CSI capture bring-up (the go/no-go gate)
1. Build a kernel with `CONFIG_VIDEO_TC358743=m` (or built-in) and `qcom-camss` enabled.
2. Write a **device tree overlay** adding the `tc358743` node on the I2C bus the C790 is wired to, with its CSI-2 endpoint linked to the Q6A CSIPHY for the 4-lane connector. Configure `data-lanes`, `clock-lanes`, the refclk, and the I2C address (TC358743 is commonly `0x0f`).
3. Set an **EDID** on the bridge (`v4l2-ctl --set-edid=...`) advertising a mode the chip can carry — start conservative (e.g. 720p60 or 1080p30), not 1080p60.
4. Bring up the media graph with `media-ctl`; confirm links CSIPHY→CSID→VFE-RDI and set UYVY format end-to-end.
5. Capture raw frames with `v4l2-ctl --stream-mmap` / `yavta`. **Success = a valid UYVY frame in RAM.** This is the gate. If the RDI raw-dump path won't carry UYVY on QCS6490 camss, escalate to the linux-media list / Linaro / Qualcomm Landing Team before sinking more time.

A *starting-point* DT sketch (adapt addresses/phandles to the real Q6A DTSI — **do not** assume this compiles):

```dts
&camss {
    status = "okay";
    ports {
        port@0 {
            csiphy0_ep: endpoint {
                remote-endpoint = <&tc358743_out>;
                data-lanes = <1 2 3 4>;
                clock-lanes = <0>;
            };
        };
    };
};

&i2c_X {                 /* the bus the C790 control lines are on */
    status = "okay";
    tc358743@f {
        compatible = "toshiba,tc358743";
        reg = <0x0f>;
        clocks = <&tc358743_refclk>;   /* 27 MHz typical */
        clock-names = "refclk";
        /* reset-gpios / interrupts as wired */
        port {
            tc358743_out: endpoint {
                remote-endpoint = <&csiphy0_ep>;
                data-lanes = <1 2 3 4>;
                clock-lanes = <0>;
            };
        };
    };
};
```

> **Update (2026-06-17, from image audit + schematic):** Two corrections.
> (a) Control I2C is the Qualcomm **CCI** bus, not a generic `&i2c_X`.
> (b) **The C790 cannot use the 4-lane connector** — the Q6A 4-lane port (CAM1)
> is a 31-pin 0.3 mm FPC, while the C790 is 22-pin/15-pin. The viable path is the
> **C790 front 15-pin → Q6A CAM2/CAM3 (2-lane, 15-pin, Pi-camera pinout)**, on
> `&cci1_i2c0` (CAM2). 2 lanes => ~1080p25-30/720p ceiling. See
> `docs/hardware.md` and the board-correct `kernel/dts/qcs6490-q6a-tc358743.dtso`.

### Phase 2 — Stream the CSI source
Point `ustreamer` at the new `/dev/videoN`. Use the QCS6490 hardware encoder for H.264 if latency/bandwidth demands it; MJPEG is the simpler first target. Tune resolution/fps to whatever the TC358743 + camss combo carries reliably.

### Phase 3 — USB gadget (HID + mass storage)
Switch the OTG port's `dr_mode` to `peripheral` in DT. Build a composite gadget via **configfs** exposing HID keyboard, HID mouse (both absolute and relative), and mass-storage. Wire it to kvmd's HID layer. The kvmd-armbian gadget scripts are your reference implementation.

### Phase 4 — Package as `kvmd` platform
Create a `kvmd-platform-*-q6a` equivalent: udev rules, gadget unit, override.yaml defaults, systemd services. Produce a flashable image + an install script (fork srepac's / kvmd-armbian's installer).

### Phase 5 — Stretch goals
ATX power/reset control via GPIO + optocouplers; I2S audio capture from the TC358743 pads → USB audio gadget; OLED/stats; CEC.

---

## 7. Known risks & realistic expectations

- **QCS6490 camss RDI/UYVY support is the #1 risk.** Architecturally supported, not yet proven by you. Phase 1 exists to settle it fast.
- **TC358743 bandwidth.** This is an older bridge; reliable **1080p60 is often not achievable** even when marketing says so. Plan for 1080p30/50 or 720p60 as the dependable target and treat higher as a bonus.
- **HDMI back-power / EDID quirks.** The C790 mitigates back-powering, but cut the USB cable's VBUS line to the target and expect to hand-tune EDID for stubborn BIOS/UEFI sources (HP/Dell are notorious).
- **Vendor kernel drift.** If Radxa's BSP diverges from the mainline camss you build against, you may need to forward-port. Pin a kernel branch early and document it.
- **Non-Pi kvmd sharp edges.** Expect to replace udev rules and platform packages; the app logic is reusable, the platform layer is not.

---

## 8. Suggested repo layout

```
q6a-kvm/
├── README.md                # quickstart + the Orange-Pi-Zero-2W dead-end note
├── docs/
│   ├── handover.md          # this document
│   ├── hardware.md          # BOM, wiring, FPC pinout, photos
│   └── bringup-log.md       # running log of Phase 1 experiments (gold for contributors)
├── kernel/
│   ├── config-fragment      # CONFIG_VIDEO_TC358743, camss, dwc3 peripheral, gadget
│   └── dts/                 # overlays: tc358743 + camss endpoint, OTG dr_mode
├── gadget/                  # configfs composite gadget scripts (HID + msd)
├── edid/                    # EDID blobs per target resolution
├── packaging/               # kvmd platform files, udev, systemd units, installer
└── image/                   # image build recipe
```

Keep `bringup-log.md` brutally honest — `media-ctl` topologies, `v4l2-ctl --log-status` dumps, what EDID worked, what fps held. That log is what lets the next person continue.

---

## 9. First commands a new contributor should run

```bash
# 1. Identify the kernel and confirm the two drivers exist
uname -a
zcat /proc/config.gz | grep -E 'TC358743|CAMSS|DWC3'

# 2. See the media topology the vendor kernel ships
sudo apt install v4l-utils media-ctl
media-ctl -p

# 3. Confirm the OTG port and current dr_mode
ls /sys/class/udc            # is there a usb device controller exposed?
```

If `/sys/class/udc` is empty, the OTG port is still in host mode → DT `dr_mode` change is your first kernel task. If `media-ctl -p` shows a camss graph, Phase 1 has a foundation.

---

## 10. Key references

- PiKVM Handbook — FAQ on non-Pi boards & the `#unofficial_ports` channel; DIY V2 CSI guide (note: CSI bridge tops out ~1080p50, no 60Hz — same chip limitation you'll hit).
- `kvmd-armbian` (xe5700 / markuspm on GitHub) — the closest existing non-Pi kvmd port; OTG + gadget reference.
- Linux kernel docs — "Qualcomm Camera Subsystem driver" (`qcom_camss`); confirms RDI raw-dump + UYVY support.
- `drivers/media/i2c/tc358743.c` and its DT binding — the bridge driver and its expected properties.
- Radxa Dragon Q6A official docs + product brief (connector map: 4-lane + 2× 2-lane CSI, OTG port, 12V power).

---

*Status: kickoff. Phase 1 is the gate — everything downstream is conventional integration once UYVY frames land in RAM.*
