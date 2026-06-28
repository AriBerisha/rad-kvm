#!/bin/bash
# q6a-kvm-stream — feed the camss capture into the v4l2loopback and serve it as
# MJPEG over HTTP with ustreamer. Run by q6a-kvm-streamer.service.
#
# camss /dev/video0 (mplane) --gst--> /dev/video20 (v4l2loopback) --ustreamer--> :8080
#
# ustreamer can't open the multiplanar camss node directly, hence the loopback.
# Resolution is auto-detected. ONE ustreamer worker: the loopback yields 2
# buffers and the rule is buffers >= workers+1.
set -u

SRC=/dev/video0
LOOP=/dev/video20
PORT=8080
log() { logger -t q6a-kvm-stream -- "$*"; echo "q6a-kvm-stream: $*"; }

# Make sure the camss pipe is configured to the LIVE source (idempotent:
# loads EDID, applies timings, routes csiphy2->csid0->vfe0_rdi0).
/usr/local/bin/q6a-kvm-configure || true

# Wait for the source node and the loopback to exist.
for _ in $(seq 20); do [ -e "$SRC" ] && [ -e "$LOOP" ] && break; sleep 1; done
[ -e "$SRC" ] && [ -e "$LOOP" ] || { log "FATAL: $SRC or $LOOP missing"; exit 1; }

# Detect the configured resolution from the camss node.
WH=$(v4l2-ctl -d "$SRC" --get-fmt-video 2>/dev/null | grep -i 'Width/Height' | grep -oE '[0-9]+/[0-9]+' | head -1)
W=${WH%/*}; H=${WH#*/}
if ! { [ "${W:-0}" -ge 320 ]; } 2>/dev/null; then
	log "no usable source resolution (${WH:-none}); will retry"
	exit 1
fi
log "streaming ${W}x${H} on :${PORT}"

# gst feeder: camss (mplane) -> loopback (single-planar)
gst-launch-1.0 -q v4l2src device="$SRC" ! video/x-raw,format=UYVY,width=${W},height=${H} ! v4l2sink device="$LOOP" sync=false &
GST=$!
trap 'kill $GST 2>/dev/null' EXIT
sleep 2
kill -0 "$GST" 2>/dev/null || { log "gst feeder failed to start"; exit 1; }

# ustreamer (foreground; its exit triggers the trap -> kills the feeder)
ustreamer --device="$LOOP" --format=UYVY --resolution=${W}x${H} --desired-fps=30 --encoder=CPU --workers=1 --quality=80 --host=0.0.0.0 --port=${PORT}
