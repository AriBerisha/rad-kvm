#!/usr/bin/env python3
"""Generate the RAD-KVM EDID -> rad-kvm.hex.

Advertises a broad set of resolutions that all fit the C790 CAM2 2-lane CSI
budget (<= 74.25 Mpix/s = 1188 Mbps / 16bpp; link-freq 297 MHz) AND are
16-pixel-aligned (camss-friendly), so a source's display settings show a useful
list — including small/panel modes — and never pick something we can't carry:

  established timings : 640x480@60, 800x600@60, 1024x768@60
  standard timings    : 1280x720@60
  detailed timings    : 1280x720@60 (preferred), 800x480@60,
                        1024x600@60, 1920x1080@30   (1080p30 also a CEA VIC)
  CEA VICs            : 720p60 (native), 720p50, 1080p30, 1080p25

Modes a 2-lane link can't carry (1280x800/1366x768/1080p50/60, …) are omitted so
EDID-honoring sources don't pick them. Monitor name = "RAD-KVM".
"""

PREFIX = bytes.fromhex(
    "00ffffffffffff00"        # header
    "52628888" "00888888"     # mfr id / product / serial (generic placeholders)
    "1c" "15" "0103"          # mfg week / year / EDID 1.3
    "80" "0000" "78" "0a"     # digital input, size unspecified, gamma 2.2, features
    "ee91a3544c99260f5054"    # chromaticity
)
assert len(PREFIX) == 35

# established timings (0x23-0x25): 720x400@70 (BIOS/DOS text) + 640x480@60 +
# 800x600@60 + 1024x768@60. bit7 of byte 0x23 = 720x400@70 (28.32 MHz) so most
# PC firmware/BIOS screens are advertised and capture cleanly.
ESTABLISHED = bytes([0xA1, 0x08, 0x00])
# standard timings (8 slots): 1280x720@60 (16:9), rest unused
STANDARD = bytes([0x81, 0xc0]) + bytes([0x01] * 14)
assert len(ESTABLISHED) == 3 and len(STANDARD) == 16


def dtd(pclk_khz, hact, hfp, hsw, hbp, vact, vfp, vsw, vbp, hmm=1600, vmm=900):
    hbl, vbl = hfp + hsw + hbp, vfp + vsw + vbp
    p = pclk_khz // 10
    d = bytearray(18)
    d[0] = p & 0xff; d[1] = (p >> 8) & 0xff
    d[2] = hact & 0xff; d[3] = hbl & 0xff
    d[4] = ((hact >> 8) & 0xf) << 4 | ((hbl >> 8) & 0xf)
    d[5] = vact & 0xff; d[6] = vbl & 0xff
    d[7] = ((vact >> 8) & 0xf) << 4 | ((vbl >> 8) & 0xf)
    d[8] = hfp & 0xff; d[9] = hsw & 0xff
    d[10] = ((vfp & 0xf) << 4) | (vsw & 0xf)
    d[11] = (((hfp >> 8) & 3) << 6) | (((hsw >> 8) & 3) << 4) | (((vfp >> 4) & 3) << 2) | ((vsw >> 4) & 3)
    d[12] = hmm & 0xff; d[13] = vmm & 0xff
    d[14] = ((hmm >> 8) & 0xf) << 4 | ((vmm >> 8) & 0xf)
    d[17] = 0x1e              # digital separate sync, +H +V, progressive
    return bytes(d)


def name_desc(s):
    b = bytearray(b'\x00\x00\x00\xfc\x00') + s.encode()[:13]
    if len(b) < 18:
        b += b'\x0a'
    b += b'\x20' * (18 - len(b))
    return bytes(b[:18])


def checksum(block):
    return bytes(block) + bytes([(256 - (sum(block) & 0xff)) & 0xff])


# all <= 74.25 MHz pixel clock (the 2-lane budget), all widths 16-aligned
dtd_720p60   = dtd(74250, 1280, 110, 40, 220, 720, 5, 5, 20)
dtd_800x480  = dtd(33260, 800, 210, 30, 16, 480, 22, 13, 10)   # ~60 Hz, ~33 MHz
dtd_1024x600 = dtd(49000, 1024, 40, 104, 144, 600, 1, 3, 28)   # ~60 Hz, ~49 MHz
dtd_1080p30  = dtd(74250, 1920, 88, 44, 148, 1080, 4, 5, 36)
# the only "bigger" panels a 2-lane link can carry — both CVT reduced-blanking,
# pclk well under the 74.25 MHz budget, widths 16-aligned
dtd_1280x768 = dtd(68250, 1280, 48, 32, 80, 768, 3, 7, 12)     # CVT-RB ~60 Hz, 68.25 MHz
dtd_1280x800 = dtd(71000, 1280, 48, 32, 80, 800, 3, 6, 14)     # CVT-RB ~60 Hz, 71.0 MHz
# monitor range: V 24-75 Hz, H 26-81 kHz, max pclk 230 MHz (covers all advertised)
range_desc = bytes.fromhex("000000fd00" "184b1a5117" "000a" "202020202020")

base = bytearray(PREFIX) + ESTABLISHED + STANDARD + dtd_720p60 + dtd_800x480 + range_desc + name_desc("RAD-KVM")
base += b'\x01'                                  # one extension block
base = checksum(base[:127])
assert len(base) == 128

vics = [0x84, 0x13, 0x22, 0x21]                  # 720p60(native), 720p50, 1080p30, 1080p25
vdb = bytes([0x40 | len(vics)]) + bytes(vics)    # CEA video data block
hdmi = bytes([0x60 | 5, 0x03, 0x0c, 0x00, 0x10, 0x00])  # HDMI VSDB, OUI 00-0C-03, phys 1.0.0.0
dbc = vdb + hdmi
cea_dtds = dtd_1080p30 + dtd_1024x600 + dtd_800x480 + dtd_1280x800 + dtd_1280x768
ndtd = 5
cea = bytearray([0x02, 0x03, 4 + len(dbc), ndtd]) + dbc + cea_dtds
cea = checksum((bytes(cea) + bytes(127))[:127])
assert len(cea) == 128

edid = base + cea
here = __file__.rsplit('/', 1)[0] if '/' in __file__ else '.'
rows = [' '.join(f'{edid[i + j]:02x}' for j in range(16)) for i in range(0, 256, 16)]
with open(here + '/rad-kvm.hex', 'w') as f:
    f.write('\n'.join(rows) + '\n')
print("wrote rad-kvm.hex (256 bytes); block checksums",
      sum(edid[0:128]) & 0xff, sum(edid[128:256]) & 0xff, "(both 0 = ok)")
print("modes: 720x400@70(BIOS) 640x480 800x480 800x600 1024x600 1024x768 1280x768 1280x800 "
      "1280x720(pref) 1080p30 +720p50/1080p25")
