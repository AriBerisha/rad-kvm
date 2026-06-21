# tc358743 — patched fork for qcom-camss (QCS6490)

`tc358743.c` and `tc358743_regs.h` are the **mainline Linux v6.18** Toshiba
TC358743 HDMI→CSI-2 bridge driver, vendored here and patched so the bridge
interoperates with the Qualcomm `qcom-camss` receiver on the Radxa Dragon Q6A.
Licensed GPL-2.0, same as upstream.

## Why a fork

Stock mainline `tc358743` streams fine on simple CSI receivers but **fails on
qcom-camss at `STREAMON`** with `-EINVAL` and
`qcom-camss: Cannot get CSI2 transmitter's link frequency`. Two gaps:

1. **No `V4L2_CID_LINK_FREQ` control.** camss (`v4l2_get_link_freq`) reads the
   CSI-2 line rate from a control on the source subdev; mainline tc358743 only
   exposes 5V-detect + audio controls.
2. **Wrong entity function.** The bridge declares `MEDIA_ENT_F_VID_IF_BRIDGE`,
   but `camss_find_sensor_pad()` walks upstream from the CSIPHY and only stops
   at a `MEDIA_ENT_F_CAM_SENSOR` — so camss never even reaches the control.

## Patches applied (vs mainline v6.18)

- Add a read-only `V4L2_CID_LINK_FREQ` integer-menu control, fed by the DT
  endpoint's `link-frequencies` (297 MHz on our overlay = the bridge's actual
  fixed CSI-2 line rate, 594 Mbps/lane). Requires the overlay endpoint to carry
  `link-frequencies` (it must anyway, or `probe_of` rejects it).
- Set `sd->entity.function = MEDIA_ENT_F_CAM_SENSOR` so camss's sensor-pad walk
  finds the bridge.
- **Chunk all I2C transfers (`i2c_rd`/`i2c_wr`) into ≤8-byte bursts** (`I2C_BURST`).
  The control I2C is the Qualcomm **CCI**, which has a small max transfer length
  and rejects long transfers with **`-EOPNOTSUPP` (-95)**. Mainline writes/reads
  the EDID in **128-byte blocks** in a single transfer, so the EDID never lands
  in the bridge's RAM (`i2c_wr: writing register 0x8c00 … failed: -95`) and every
  HDMI source then reads an **empty EDID** and falls back to its default mode
  (e.g. 1080p60, which the 2-lane link can't carry). The chip auto-increments the
  register address, so splitting into bursts is transparent. **This is what makes
  the EDID — and therefore source-resolution steering — work at all on CCI.**

`camss_get_pixel_clock` failures are non-fatal in camss (`if (ret)
pixel_clock[i] = 0`), so no `V4L2_CID_PIXEL_RATE` is needed.

## Build / install (on the board)

One-shot: [`scripts/build-tc358743.sh`](../../scripts/build-tc358743.sh) — builds
**from this vendored source** (so all patches, incl. the CCI I2C fix, are
included) against `/lib/modules/$(uname -r)/build`, installs, depmods. Reboot to
load. Manual:

```sh
make -C /lib/modules/$(uname -r)/build M=$PWD modules
sudo make -C /lib/modules/$(uname -r)/build M=$PWD modules_install
sudo depmod -a && sudo reboot   # live rmmod is refused while camss is bound
```

> Upstreaming note: report the link-frequency value via `get_mbus_config`
> (`v4l2_mbus_config.link_freq`) instead of a fixed control, and reconsider the
> entity-function override, before proposing this anywhere upstream.
