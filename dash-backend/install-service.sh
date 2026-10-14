#!/bin/bash
set -euo pipefail
if [[ "$(id -u)" != 0 ]]; then
  echo 'Run: sudo bash /data/dash/install-service.sh'
  exit 1
fi
if [[ ! -f /sys/fs/cgroup/cgroup.controllers ]]; then
  echo 'Dash requires unified cgroup v2. This AGNOS configuration cannot enforce the full policy.'
  echo 'Installation stopped. No kernel or firmware settings were changed.'
  exit 1
fi
for DASH_CONTROLLER in cpu memory pids; do
  if ! tr ' ' '\n' < /sys/fs/cgroup/cgroup.controllers | grep -qx "$DASH_CONTROLLER"; then
    echo "Missing cgroup controller: $DASH_CONTROLLER. Service will not be installed."
    exit 1
  fi
done
if ! command -v ffmpeg >/dev/null || ! command -v ffprobe >/dev/null || ! command -v systemctl >/dev/null; then
  echo 'Installation requires ffmpeg, ffprobe and systemd.'
  exit 1
fi
ffmpeg -hide_banner -encoders 2>/dev/null | grep -F libx264 >/dev/null
if ! ffmpeg -hide_banner -h full 2>/dev/null | grep -F -- '-fps_mode' >/dev/null; then
  echo 'FFmpeg with -fps_mode support (5 or later) is required for MCAP video conversion.'
  exit 1
fi
DASH_BITSTREAM_FILTERS="$(ffmpeg -hide_banner -bsfs 2>/dev/null)"
for DASH_FILTER in h264_metadata hevc_metadata; do
  if ! grep -Eq "^[[:space:]]*$DASH_FILTER[[:space:]]*$" <<< "$DASH_BITSTREAM_FILTERS"; then
    echo "FFmpeg requires $DASH_FILTER for playable MCAP video."
    exit 1
  fi
done
id comma >/dev/null
mkdir -p /data/dash/cache
chown comma:comma /data/dash/cache
chmod 700 /data/dash/cache
systemd-analyze verify /data/dash/systemd/dash.service
install -m 644 /data/dash/systemd/dash.service /etc/systemd/system/dash.service
systemctl daemon-reload
systemctl reset-failed dash.service
systemctl enable dash.service
if ! systemctl restart dash.service; then
  systemctl disable --now dash.service
  journalctl -u dash.service -n 30 --no-pager
  exit 1
fi
DASH_HEALTHY=false
for DASH_ATTEMPT in {1..55}; do
  if bash /data/dash/verify-service.sh >/dev/null 2>&1; then DASH_HEALTHY=true; break; fi
  if ! systemctl is-active --quiet dash.service; then break; fi
  sleep 1
done
if ! "$DASH_HEALTHY" || ! bash /data/dash/verify-service.sh; then
  systemctl disable --now dash.service
  echo 'Startup health/resource verification failed. Dash has been stopped and disabled.'
  journalctl -u dash.service -n 30 --no-pager
  exit 1
fi
echo 'Dash is enabled at boot. Logs: journalctl -u dash.service -f'
