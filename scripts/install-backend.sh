#!/bin/bash
set -euo pipefail
DASH_MODE=install
if [[ "${1:-}" == --check ]]; then DASH_MODE=check; shift; fi
DASH_TARGET="${1:-}"
if [[ ! "$DASH_TARGET" =~ ^[a-zA-Z0-9._-]+@[a-zA-Z0-9._-]+$ || $# != 1 ]]; then
  echo 'Usage: bash scripts/install-backend.sh [--check] comma@DEVICE_IP'
  echo '--check is read-only: verify device requirements without installing or starting Dash.'
  exit 2
fi
DASH_PROJECT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$DASH_PROJECT"
DASH_SSH_OPTIONS=(-o BatchMode=yes -o ConnectTimeout=10)
# This block is intentionally read-only and never changes driving params.
ssh "${DASH_SSH_OPTIONS[@]}" "$DASH_TARGET" bash -s <<'PREFLIGHT'
set -euo pipefail
[[ "$(uname -s)" == Linux ]]
case "$(uname -m)" in aarch64|arm64|x86_64) ;; *) echo 'Unsupported device architecture'; exit 1 ;; esac
[[ -f /sys/fs/cgroup/cgroup.controllers ]] || { echo 'Unified cgroup v2 is required'; exit 1; }
for controller in cpu memory pids; do
  tr ' ' '\n' < /sys/fs/cgroup/cgroup.controllers | grep -qx "$controller" || { echo "Missing controller: $controller"; exit 1; }
done
[[ "$(cat /proc/1/comm)" == systemd ]] || { echo 'systemd must manage device startup'; exit 1; }
command -v systemctl >/dev/null
command -v systemd-analyze >/dev/null
command -v ffmpeg >/dev/null
command -v ffprobe >/dev/null
ffmpeg -hide_banner -encoders 2>/dev/null | grep -F libx264 >/dev/null
id comma >/dev/null
sudo -n true
[[ -d /data/media/0/realdata && -d /data/params/d ]]
[[ "$(cat /data/params/d/IsOnroad)" == 0 && "$(cat /data/params/d/IsOffroad)" == 1 ]] || { echo 'Install only with confirmed parked state'; exit 1; }
[[ -r /data/media/0/realdata && -w /data ]]
available=$(df -Pk /data | awk 'NR==2 {print $4}')
(( available >= 2097152 )) || { echo 'At least 2 GiB free /data storage required'; exit 1; }
echo 'Preflight passed: parked, architecture, systemd, cgroup controllers, FFmpeg/H.264, permissions, sudo, and free space.'
PREFLIGHT
if [[ "$DASH_MODE" == check ]]; then exit 0; fi
DASH_ARCH="$(ssh "${DASH_SSH_OPTIONS[@]}" "$DASH_TARGET" uname -m)"
case "$DASH_ARCH" in aarch64|arm64) DASH_GOARCH=arm64 ;; x86_64) DASH_GOARCH=amd64 ;; esac
npm ci
npm run build
mkdir -p .artifacts
(cd dash-backend && CGO_ENABLED=0 GOOS=linux GOARCH="$DASH_GOARCH" go build -trimpath -ldflags='-s -w' -o ../.artifacts/dash-backend-device .)
ssh "${DASH_SSH_OPTIONS[@]}" "$DASH_TARGET" 'mkdir -p /data/dash/systemd; rm -rf -- /data/dash/dist.new; mkdir -p /data/dash/dist.new'
scp -o BatchMode=yes -o ConnectTimeout=10 dash-backend/install-service.sh dash-backend/verify-service.sh "$DASH_TARGET:/data/dash/"
scp -o BatchMode=yes -o ConnectTimeout=10 dash-backend/systemd/dash.service "$DASH_TARGET:/data/dash/systemd/"
scp -o BatchMode=yes -o ConnectTimeout=10 .artifacts/dash-backend-device "$DASH_TARGET:/data/dash/dash-backend.new"
scp -o BatchMode=yes -o ConnectTimeout=10 -r dist/. "$DASH_TARGET:/data/dash/dist.new/"
ssh "${DASH_SSH_OPTIONS[@]}" "$DASH_TARGET" bash -s <<'ACTIVATE'
set -euo pipefail
[[ "$(cat /data/params/d/IsOnroad)" == 0 && "$(cat /data/params/d/IsOffroad)" == 1 ]] || { echo 'Device no longer parked; staged files left inactive'; exit 1; }
if systemctl cat dash.service >/dev/null 2>&1; then sudo -n systemctl stop dash.service; fi
rm -f -- /data/dash/dash-backend.previous
if [[ -f /data/dash/dash-backend ]]; then mv /data/dash/dash-backend /data/dash/dash-backend.previous; fi
mv /data/dash/dash-backend.new /data/dash/dash-backend
chmod +x /data/dash/dash-backend
rm -rf -- /data/dash/dist.previous
if [[ -d /data/dash/dist ]]; then mv /data/dash/dist /data/dash/dist.previous; fi
mv /data/dash/dist.new /data/dash/dist
if ! sudo -n bash /data/dash/install-service.sh; then
  sudo -n systemctl disable --now dash.service || true
  # Restore the previous assets but keep the service disabled after failure.
  if [[ -f /data/dash/dash-backend.previous ]]; then mv /data/dash/dash-backend.previous /data/dash/dash-backend; fi
  if [[ -d /data/dash/dist.previous ]]; then rm -rf -- /data/dash/dist; mv /data/dash/dist.previous /data/dash/dist; fi
  echo 'Install failed. Previous assets restored if present; service stays stopped.'
  exit 1
fi
ACTIVATE
echo "Installed. Open http://${DASH_TARGET#*@}:8088 on the same LAN or device hotspot."
echo 'Lifecycle: sudo systemctl {start|stop|restart|status} dash.service'
