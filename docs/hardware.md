# Hardware notes

## Bill of materials
See handover §4. Headline items: Radxa Dragon Q6A (QCS6490), Geekworm C790
(TC358743) HDMI->CSI bridge, **15-pin 1.0 mm FPC cable** (see wiring below —
*not* the 22-pin cable the handover assumed), USB-A↔A cable with the +5V/VBUS
line cut, 12V PSU, HDMI cable.

## ⚠️ Connector reality (corrects handover §1/§4)

The handover assumed "the C790's 22-pin 4-lane output maps cleanly onto the Q6A
4-lane connector." **It does not.** Verified from Radxa schematic v1.21 (p.32)
and the camera-accessory docs:

| Q6A port | Lanes | Connector | DT (camss / cci) | SoC nets |
|---|---|---|---|---|
| **CAM1** (J14) | 4 | **31-pin, 0.3 mm** (`CAM_31P`, has 5V pins) | `port@0` csiphy0 / `cci0_i2c0` | CSI0 |
| **CAM2** (J16) | 2 | **15-pin, 1.0 mm** | `port@2` csiphy2 / `cci1_i2c0` | CSI2 |
| **CAM3** (J7)  | 2 | **15-pin, 1.0 mm** | `port@3` csiphy3 / `cci1_i2c1` | CSI3 |

The **C790 has two output connectors**: a **front 15-pin (1.0 mm)** (Pi 4/3
pinout, 2-lane) and a **back 22-pin (0.5 mm)** (Pi 5/CM4 pinout, up to 4-lane).
Neither the 22-pin nor 31-pin sides are mutually compatible. **The only viable
path is C790 front 15-pin → Q6A CAM2 or CAM3 (15-pin), 2-lane.**

### Why the 15-pin path works: Q6A CAM2/CAM3 use the Raspberry Pi camera pinout
From schematic v1.21 p.32, the 2-lane connector J16 (CAM2):

| Pin | Signal | Pin | Signal |
|---|---|---|---|
| 1 | GND | 9 | CSI2_NC_CLK_P (clk+) |
| 2 | CSI2_C0_LN0_M (D0−) | 10 | GND |
| 3 | CSI2_B0_LN0_P (D0+) | 11 | CAM2_RESET_3V3 (GPIO) |
| 4 | GND | 12 | CAM_MCLK2 — **NC (R358 not fitted)** |
| 5 | CSI2_B1_LN1_M (D1−) | 13 | CCI_I2C2_SCL |
| 6 | CSI2_A1_LN1_P (D1+) | 14 | CCI_I2C2_SDA |
| 7 | GND | 15 | **VCC_3V3** |
| 8 | CSI2_A0_CLK_M (clk−) | | |

This matches the standard Pi 15-pin camera FFC pinout pin-for-pin, so the C790
(a Pi-pinout device, powered from connector 3V3) is electrically compatible.
CAM3 (J7) is identical but on CSI3 / CCI_I2C3. Note: **no 5V on the 15-pin** —
power is 3V3 only (Pi-camera style); the C790 runs off this.

## Wiring procedure (C790 → Q6A CAM2)

1. Use the **C790 FRONT 15-pin (1.0 mm)** connector — leave the 22-pin back one
   unused.
2. Use a **15-pin 1.0 mm FPC cable**. Radxa's own cameras use the "opposite
   sides" contact orientation; the C790/Pi cable orientation must result in
   pin 1↔pin 1. **Get the flip wrong and you swap 3V3 (pin 15) with GND
   (pin 1).**
3. **Before powering:** continuity-check pin 1 (GND) and pin 15 (3V3) end-to-end
   across the seated cable. Confirm they are NOT crossed.
4. Plug into **CAM2** (the overlay default; `kernel/dts/qcs6490-q6a-tc358743.dtso`).
   CAM3 also works — see the overlay header for the port@3/cci1_i2c1 variant.
   **Foolproofing:** the 15-pin cable only physically fits the two 2-lane CSI
   ports (CAM2 J16 / CAM3 J7). It cannot mis-seat into the 4-lane CSI (31-pin
   J14) or the MIPI DSI display connector (**40-pin J10**, carries 5V + LED
   backlight power) — both are the wrong size.
5. Bandwidth: 2 lanes => realistic ceiling ~**1080p25-30 / 720p**. Fine for KVM
   and consistent with the C790's "1080p25" marketing. (4-lane 1080p60 is not
   reachable on this path.)

## USB / power (from product brief rev 1.5)
- OTG port = the **USB 3.1 Type-A "down" port** (`&usb_1`, set to peripheral via
  the `usb-peripheral` overlay). The "up" USB 2.0 is host. Plus 3× USB 2.0 host.
- Power: **12V external power connector** *or* USB-C PD. (Board also exposes
  2×5V / 2×3.3V on the 40-pin header.)
- Cut/leave-open the USB-A↔A cable's **+5V (VBUS)** line to the target.

## Audio / I2S (Phase 5 stretch — NOT needed for the KVM)
The C790 ships a separate 15 cm I2S cable for HDMI audio. It is **not required**
for video + HID and should be left unconnected during bring-up. If audio is
wanted later: the Q6A exposes **MI2S0** (SCK/WS/DATA0, 3V3) on the **40-pin GPIO
header** (schematic v1.21 p.24) — a 4-wire link (BCLK/LRCLK/DATA/GND) from the
C790 I2S pads plus a DT sound-card setup. Work out exact header pins then.

## EDID
Expect to hand-tune EDID for stubborn UEFI/BIOS sources. Start conservative
(720p60 / 1080p30).
