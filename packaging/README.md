# packaging — boot persistence + streaming

Brings the whole KVM video path up on boot, hands-free: `/dev/video0` configured
and a live MJPEG stream at `http://<board>:8080`.

| file | installed to | role |
|------|--------------|------|
| `q6a-kvm-configure.sh` | `/usr/local/bin/q6a-kvm-configure` | loads EDID, detects timings, routes + formats the `tc358743 → CSIPHY2 → CSID0 → VFE0_RDI0 → /dev/videoN` pipeline (idempotent, auto-detects resolution) |
| `q6a-kvm-pipeline.service` | `/etc/systemd/system/` | oneshot that runs the configure script at boot |
| `q6a-kvm-stream.sh` | `/usr/local/bin/q6a-kvm-stream` | gst feeder (camss → v4l2loopback) + ustreamer; auto-detects resolution |
| `q6a-kvm-streamer.service` | `/etc/systemd/system/` | runs the streamer (After=pipeline, Restart=always) |
| `q6a-kvm-watchdog.sh` | `/usr/local/bin/q6a-kvm-watchdog` | auto-resync: re-kicks the streamer when the source changes (resolution / sleep-wake / reboot / no-signal-at-boot / stall) |
| `q6a-kvm-watchdog.service` | `/etc/systemd/system/` | runs the watchdog (After=streamer, Restart=always) |
| `v4l2loopback.modprobe.conf` | `/etc/modprobe.d/q6a-kvm-v4l2loopback.conf` | loopback options: `/dev/video20`, `max_buffers=8`, `exclusive_caps=1` |
| `v4l2loopback.load.conf` | `/etc/modules-load.d/q6a-kvm-v4l2loopback.conf` | autoload the loopback at boot |
| `edid/rad-kvm.hex` | `/etc/q6a-kvm/edid.hex` | **RAD-KVM multi-resolution** EDID: advertises 640×480 / **800×480** / 800×600 / **1024×600** / 1024×768 / 1280×720 (preferred) / 1080p30 (+720p50 / 1080p25) — a broad set incl. small/panel modes, all **carryable + 16-aligned**, so a source's display settings give a useful list and never pick something over the 2-lane budget (≈74 Mpix/s). Regenerate with `edid/make-edid.py`. Monitor name shows as "RAD-KVM". (`tc358743-720p.hex` / `tc358743-1080p.hex` are older, reference only.) |

Install (on the board): run the generated `tmp/board-deploy-persistence.sh`,
`tmp/board-deploy-streamer.sh`, then `tmp/board-deploy-watchdog.sh`. Then test:

```sh
systemctl status q6a-kvm-pipeline q6a-kvm-streamer
journalctl -t q6a-kvm -u q6a-kvm-streamer -n 30 --no-pager
# browser: http://<board>:8080
```

**Prereqs** (built from source — apt versions too old for kernel 6.18):
`v4l2loopback` v0.15.3 and `ustreamer` v6.60. ustreamer needs ≥6.x (5.4 aborts
on the loopback); ustreamer runs `--workers=1` because the loopback exposes 2
buffers and the rule is buffers ≥ workers+1.

## Scope / limitations

- The **`q6a-kvm-watchdog`** service handles a changing source: it polls the
  bridge DV-timings + ustreamer fps and re-kicks the streamer (~6s blip) when the
  target changes resolution, sleeps/wakes, reboots, comes up after a no-signal
  boot, or the capture stalls — so the video follows the target hands-free.
  (Debounced; min 8s between re-kicks.) A manual re-kick is still
  `sudo systemctl restart q6a-kvm-streamer`.
- Assumes the patched `tc358743` module and the CAM2 overlay (on the ESP) are in
  place — see the repo root README.
