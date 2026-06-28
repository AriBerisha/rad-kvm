# gadget — USB HID + mass-storage composite gadget

The "control" half of the KVM: the board presents itself to the **target** as a
USB keyboard, mouse, and removable drive, over the OTG port (`usb@a600000`) in
peripheral mode. Pure configfs/libcomposite — the entire gadget stack is already
compiled in the vendor kernel (`6.18.2-3-qcom`), so there is nothing to build.

| file | installed to | role |
|------|--------------|------|
| `q6a-kvm-gadget.sh` | `/usr/local/bin/q6a-kvm-gadget` | builds + binds the composite gadget (idempotent: tears down and rebuilds). Waits for the UDC first. |
| `q6a-kvm-gadget.service` | `/etc/systemd/system/` | oneshot that runs the gadget at boot (`WantedBy=multi-user.target`) |

## What it exposes

One composite gadget (`idVendor 0x1d6b` / `idProduct 0x0104`, IAD class) bound to
the `a600000.usb` UDC, with these functions → character devices:

| node | function | report | notes |
|------|----------|--------|-------|
| `/dev/hidg0` | HID keyboard | 8-byte boot report | `[mods][resv][k1..k6]`, LED output report |
| `/dev/hidg1` | HID mouse **absolute** | 6 bytes | `[buttons][x:0-32767][y:0-32767][wheel]` |
| `/dev/hidg2` | HID mouse **relative** | 4 bytes | `[buttons][dx][dy][wheel]` |
| `mass_storage.usb0` | removable disk | — | backing file `/var/lib/q6a-kvm/msd.img` (64 MB, vfat) |

Report descriptors match PiKVM/kvmd so kvmd's HID layer can drive these nodes
unmodified. Quick smoke test (relative mouse jiggle): `printf '\x00\x0c\x00\x00'
> /dev/hidg2`.

## Prerequisites (one-time)

1. **OTG port in peripheral mode.** Enable the vendor `usb-peripheral` overlay so
   `usb@a600000` gets `dr_mode=peripheral` and a UDC appears in `/sys/class/udc`.
   This board boots via **systemd-boot**, so enable it through `/boot/dtbo` +
   `rsetup` (or `u-boot-update` + the loader-entry `devicetree-overlay` line) —
   *not* by editing the ESP `dtbo/` dir. See the root README / bring-up log.
2. **A USB-A host on the target side.** The board's only USB-C is power-only; the
   gadget rides the **blue USB-A** port. Because a USB-A connector advertises
   "host," a USB-C-only target won't power/host it directly — put a **hub or a
   USB-C→USB-A adapter** between them and use an A-to-A cable to the blue port.
   `cat /sys/class/udc/a600000.usb/state` should read `configured` when connected.

## Status / limitations

- Verified on a Mac: enumerates as "q6a-kvm Composite KVM" (keyboard + mouse +
  64 MB disk) and the cursor responds to `/dev/hidg2` writes.
- Mass-storage image is a static 64 MB vfat blob; live image swapping / CD-ROM
  emulation (for OS install media) is a follow-up, best handled by kvmd's MSD layer.
- Not yet wired to **kvmd** — that's the next phase (browser keyboard/mouse + MSD).
