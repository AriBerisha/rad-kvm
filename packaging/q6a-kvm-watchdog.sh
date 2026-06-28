#!/bin/bash
# q6a-kvm capture watchdog.
#
# The camss pipeline configures once and does not follow the source, so the
# video freezes whenever the target changes its output: resolution change,
# display sleep/wake, reboot, BIOS<->OS handoff, or no-signal-at-boot. This
# watchdog detects those and re-kicks the streamer (which re-runs the configure
# at the new timings), so the video follows the target hands-free.
#
# It also recognizes a source mode the 2-lane CAM2 link CANNOT carry (pixelclock
# above the budget, e.g. 1080p50/60): it logs that once and BACKS OFF instead of
# re-kicking forever, so a too-high source doesn't cause a restart storm.
#
# Triggers (debounced):
#   * signal returns after absence (sleep/wake + no-source-at-boot)
#   * source resolution != camss config (carryable mode change)
#   * captured fps stuck at 0 while a CARRYABLE signal is present (stall)
set -u

POLL=2          # seconds between checks
STALL_TICKS=3   # consecutive fps==0 ticks (carryable) before acting (~6s)
MIN_INTERVAL=8  # min seconds between re-kicks (anti-thrash)
MAXPCLK=75000000 # pixelclock ceiling for the 2-lane link (74.25 MHz = 720p60/1080p30,
                 # + a hair of slack). Above this the CSI physically can't carry it.
STATE_URL=http://127.0.0.1:8080/state

log() { echo "q6a-kvm-watchdog: $*"; }

bridge_sd() { media-ctl -e 'tc358743 18-000f' 2>/dev/null; }

bridge_timings() { # -> "WxH" of the live source, or empty if no signal
  local sd; sd=$(bridge_sd); [ -n "$sd" ] || return 0
  v4l2-ctl -d "$sd" --query-dv-timings 2>/dev/null | awk '
    /Active width/  { w=$NF }
    /Active height/ { h=$NF }
    END { if (w && h) print w "x" h }'
}

bridge_pclk() { # -> pixelclock Hz of the live source (empty if none)
  local sd; sd=$(bridge_sd); [ -n "$sd" ] || return 0
  v4l2-ctl -d "$sd" --query-dv-timings 2>/dev/null | awk '/Pixelclock/{print $2; exit}'
}

cam_res() { v4l2-ctl -d /dev/video0 --get-fmt-video-mplane 2>/dev/null | awk '/Width\/Height/{print $3}' | tr '/' 'x'; }
ust_fps() { curl -s --max-time 2 "$STATE_URL" 2>/dev/null | tr -d ' ' | grep -o '"captured_fps":[0-9]*' | grep -o '[0-9]*$'; }

rekick() {
  log "re-kicking streamer: $1"
  systemctl restart q6a-kvm-streamer
  sleep 6
}

last=$(date +%s)
stall=0
have=$([ -n "$(bridge_timings)" ] && echo 1 || echo 0)
unsup=""
log "started (poll=${POLL}s, stall=${STALL_TICKS}, min_interval=${MIN_INTERVAL}s, max_pclk=${MAXPCLK}Hz)"

while true; do
  sleep "$POLL"
  t=$(bridge_timings)
  now=$(date +%s); since=$((now - last))

  if [ -z "$t" ]; then
    have=0; stall=0; unsup=""; continue        # no signal — wait
  fi

  pc=$(bridge_pclk)
  if [ -n "$pc" ] && [ "$pc" -gt "$MAXPCLK" ]; then
    if [ "$unsup" != "${t}@${pc}" ]; then
      log "UNSUPPORTED source ${t} (pixelclock ${pc} Hz > 2-lane budget) — set source <=720p60 / 1080p30. Not re-kicking."
      unsup="${t}@${pc}"
    fi
    have=1; stall=0; continue                   # back off — do NOT re-kick a mode we can't carry
  fi
  unsup=""

  if [ "$have" = 0 ]; then                       # carryable signal just (re)appeared
    have=1; stall=0
    [ "$since" -ge "$MIN_INTERVAL" ] && { rekick "signal returned (${t})"; last=$(date +%s); }
    continue
  fi

  cam=$(cam_res)
  if [ -n "$cam" ] && [ "$t" != "$cam" ]; then   # carryable resolution change
    stall=0
    [ "$since" -ge "$MIN_INTERVAL" ] && { rekick "resolution ${cam} -> ${t}"; last=$(date +%s); }
    continue
  fi

  fps=$(ust_fps)
  if [ "${fps:-0}" = 0 ]; then
    stall=$((stall + 1))
    if [ "$stall" -ge "$STALL_TICKS" ] && [ "$since" -ge "$MIN_INTERVAL" ]; then
      rekick "capture stall (fps=0 for ${stall} ticks)"; last=$(date +%s); stall=0
    fi
  else
    stall=0
  fi
done
