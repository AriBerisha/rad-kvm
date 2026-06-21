#!/bin/bash
# End-to-end: EDID -> source lock -> apply timings -> bridge UYVY -> route the
# RDI pipe -> capture. Run AFTER the patched tc358743 (with LINK_FREQ) is loaded.
set -u
M=/dev/media0
SD=$(media-ctl -e 'tc358743 18-000f')
echo "bridge subdev: $SD"

echo "== 1) load EDID so the source transmits =="
cat > /tmp/q6a-edid.hex <<'EOF'
00 ff ff ff ff ff ff 00 52 62 88 88 00 88 88 88
1c 15 01 03 80 00 00 78 0a ee 91 a3 54 4c 99 26
0f 50 54 00 00 00 01 01 01 01 01 01 01 01 01 01
01 01 01 01 01 01 02 3a 80 18 71 38 2d 40 58 2c
45 00 20 c2 31 00 00 1e 00 00 00 ff 00 31 32 33
34 0a 20 20 20 20 20 20 20 20 00 00 00 fd 00 18
4b 1a 51 17 00 0a 20 20 20 20 20 20 00 00 00 fc
00 54 6f 73 68 69 62 61 2d 48 32 43 0a 20 01 f1
02 03 1a 72 47 5f 10 1f 04 13 22 21 20 01 23 09
07 07 83 01 00 00 65 03 0c 00 10 00 8c 0a d0 8a
20 e0 2d 10 10 3e 96 00 58 c2 21 00 00 18 01 1d
80 18 71 1c 16 20 58 2c 25 00 20 c2 31 00 00 9e
01 1d 00 72 51 d0 1e 20 6e 28 55 00 20 c2 31 00
00 1e 8c 0a d0 8a 20 e0 2d 10 10 3e 96 00 20 c2
31 00 00 18 00 00 00 00 00 00 00 00 00 00 00 00
00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 5f
EOF
v4l2-ctl -d "$SD" --set-edid=file=/tmp/q6a-edid.hex --fix-edid-checksums
echo "   waiting for source lock..."
for i in $(seq 10); do v4l2-ctl -d "$SD" --query-dv-timings >/dev/null 2>&1 && break; sleep 1; done

echo "== 2) apply detected timings, read resolution =="
v4l2-ctl -d "$SD" --set-dv-bt-timings query
W=$(v4l2-ctl -d "$SD" --get-dv-timings | awk '/Active width/{print $3}')
H=$(v4l2-ctl -d "$SD" --get-dv-timings | awk '/Active height/{print $3}')
echo "   detected ${W}x${H}"
[ -n "$W" ] && [ "$W" -gt 0 ] || { echo "FATAL: no lock — replug HDMI / set Mac display mode"; exit 1; }

echo "== 3) bridge -> UYVY, then route + format the RDI pipe =="
media-ctl -d "$M" -V "'tc358743 18-000f':0 [fmt:UYVY8_1X16/${W}x${H} field:none]"
media-ctl -d "$M" -r
media-ctl -d "$M" -l "'msm_csiphy2':1 -> 'msm_csid0':0 [1]"
media-ctl -d "$M" -l "'msm_csid0':1 -> 'msm_vfe0_rdi0':0 [1]"
for P in "'msm_csiphy2':0" "'msm_csiphy2':1" "'msm_csid0':0" "'msm_csid0':1" "'msm_vfe0_rdi0':0" "'msm_vfe0_rdi0':1"; do
  media-ctl -d "$M" -V "$P [fmt:UYVY8_1X16/${W}x${H} field:none]"
done

echo "== 4) capture 30 frames from /dev/video0 =="
v4l2-ctl -d /dev/video0 --set-fmt-video=width=${W},height=${H},pixelformat=UYVY
rm -f /tmp/cap.uyvy
v4l2-ctl -d /dev/video0 --stream-mmap --stream-count=30 --stream-to=/tmp/cap.uyvy
BYTES=$(wc -c < /tmp/cap.uyvy 2>/dev/null || echo 0)
FRAME=$((W*H*2))
echo "----------------------------------------------------------------"
echo "captured: $BYTES bytes ; one ${W}x${H} UYVY frame = $FRAME"
if [ "$FRAME" -gt 0 ]; then echo "whole frames: $((BYTES/FRAME)) ; remainder: $((BYTES%FRAME))"; fi
echo "----------------------------------------------------------------"
if [ "$BYTES" -ge "$FRAME" ] && [ "$FRAME" -gt 0 ]; then
  echo ">>> GATE PASS: VFE RDI is delivering UYVY frames."
  if command -v ffmpeg >/dev/null; then
    head -c "$FRAME" /tmp/cap.uyvy > /tmp/frame0.uyvy
    ffmpeg -v error -y -f rawvideo -pix_fmt uyvy422 -s ${W}x${H} -i /tmp/frame0.uyvy -frames:v 1 /tmp/frame0.png \
      && echo ">>> wrote /tmp/frame0.png (scp to Mac to eyeball)"
  fi
else
  echo ">>> no frames — paste the output and the last dmesg lines."
fi
