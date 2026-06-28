#!/bin/bash
# q6a-kvm-configure — load the TC358743 EDID and bring up the
# TC358743 -> CSIPHY2 -> CSID0 -> VFE0_RDI0 -> /dev/videoN UYVY capture pipeline.
#
# Idempotent; run at boot by q6a-kvm-pipeline.service. Auto-detects the source
# resolution. Exits 0 even when no HDMI source is locked (the EDID is still
# loaded; re-run `systemctl start q6a-kvm-pipeline` once a source is present).
set -u

MEDIA=/dev/media0
EDID=/etc/q6a-kvm/edid.hex
BRIDGE='tc358743 18-000f'
log() { logger -t q6a-kvm -- "$*"; echo "q6a-kvm: $*"; }

# 1) Wait for the bridge subdev + media device (module autoloads from the overlay)
SD=""
for _ in $(seq 30); do
	SD=$(media-ctl -e "$BRIDGE" 2>/dev/null || true)
	[ -n "$SD" ] && [ -e "$MEDIA" ] && break
	sleep 1
done
[ -n "$SD" ] || { log "FATAL: bridge subdev '$BRIDGE' not found"; exit 1; }

# 2) Load EDID (static; required for the HDMI source to transmit at all)
if [ -r "$EDID" ]; then
	v4l2-ctl -d "$SD" --set-edid=file="$EDID" --fix-edid-checksums >/dev/null 2>&1 \
		&& log "EDID loaded from $EDID" || log "WARN: EDID load failed"
else
	log "WARN: no EDID at $EDID"
fi

# 3) Wait briefly for a locked source; bail cleanly if none yet
locked=0
for _ in $(seq 8); do
	v4l2-ctl -d "$SD" --query-dv-timings >/dev/null 2>&1 && { locked=1; break; }
	sleep 1
done
if [ "$locked" != 1 ]; then
	log "no HDMI source locked; EDID is set, pipeline left unconfigured"
	exit 0
fi

# 4) Apply detected timings, read resolution
v4l2-ctl -d "$SD" --set-dv-bt-timings query >/dev/null 2>&1
W=$(v4l2-ctl -d "$SD" --get-dv-timings | awk '/Active width/{print $3}')
H=$(v4l2-ctl -d "$SD" --get-dv-timings | awk '/Active height/{print $3}')
{ [ -n "${W:-}" ] && [ "$W" -gt 0 ]; } 2>/dev/null || { log "FATAL: bad timings (${W:-?}x${H:-?})"; exit 1; }

# 5) Bridge -> UYVY, route the RDI pipe, match UYVY8_1X16/WxH on every pad
FMT="fmt:UYVY8_1X16/${W}x${H} field:none"
media-ctl -d "$MEDIA" -V "'$BRIDGE':0 [$FMT]"
media-ctl -d "$MEDIA" -r
media-ctl -d "$MEDIA" -l "'msm_csiphy2':1 -> 'msm_csid0':0 [1]"
media-ctl -d "$MEDIA" -l "'msm_csid0':1 -> 'msm_vfe0_rdi0':0 [1]"
for P in "'msm_csiphy2':0" "'msm_csiphy2':1" "'msm_csid0':0" "'msm_csid0':1" \
         "'msm_vfe0_rdi0':0" "'msm_vfe0_rdi0':1"; do
	media-ctl -d "$MEDIA" -V "$P [$FMT]"
done

# 6) Set the capture node format (resolve the node from its entity name)
VIDEO=$(media-ctl -d "$MEDIA" -e 'msm_vfe0_video0')
v4l2-ctl -d "$VIDEO" --set-fmt-video=width="$W",height="$H",pixelformat=UYVY >/dev/null 2>&1

log "pipeline ready: ${W}x${H} UYVY on ${VIDEO}"
