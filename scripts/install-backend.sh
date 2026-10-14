#!/bin/bash
set -euo pipefail
DASH_MODE=install
DASH_CHANNEL=main
DASH_RELEASE=''
DASH_REPOSITORY=LBYPatrick/dash
DASH_TARGET=''
DASH_SOURCE=false
DASH_FEED_OPTION=false
while (( $# )); do
  case "$1" in
    --check) DASH_MODE=check; shift ;;
    --source) DASH_SOURCE=true; shift ;;
    --channel|--release|--repo)
      DASH_FEED_OPTION=true
      if (( $# < 2 )); then echo "Missing value for $1"; exit 2; fi
      case "$1" in --channel) DASH_CHANNEL="$2" ;; --release) DASH_RELEASE="$2" ;; --repo) DASH_REPOSITORY="$2" ;; esac
      shift 2 ;;
    --*) echo "Unknown option: $1"; exit 2 ;;
    *) if [[ -n "$DASH_TARGET" ]]; then echo 'Specify one SSH target'; exit 2; fi; DASH_TARGET="$1"; shift ;;
  esac
done
if [[ ! "$DASH_TARGET" =~ ^[a-zA-Z0-9._-]+@[a-zA-Z0-9._-]+$ ]]; then
  echo 'Usage: bash scripts/install-backend.sh [--check] [--source | --channel main|nightly|dev] [--release TAG] [--repo OWNER/REPO] comma@DEVICE_IP'
  exit 2
fi
if "$DASH_SOURCE" && "$DASH_FEED_OPTION"; then echo '--source cannot be combined with channel/release/repository options'; exit 2; fi
case "$DASH_CHANNEL" in main|nightly|dev) ;; *) echo 'Invalid channel'; exit 2 ;; esac
if [[ ! "$DASH_REPOSITORY" =~ ^[a-zA-Z0-9_-][a-zA-Z0-9_.-]*/[a-zA-Z0-9_-][a-zA-Z0-9_.-]*$ || ( -n "$DASH_RELEASE" && ! "$DASH_RELEASE" =~ ^[a-zA-Z0-9][a-zA-Z0-9.+_-]{0,127}$ ) ]]; then echo 'Invalid repository or release tag'; exit 2; fi
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
sudo -n true
[[ -d /data/media/0/realdata && -d /data/params/d ]]
[[ "$(cat /data/params/d/IsOnroad)" == 0 && "$(cat /data/params/d/IsOffroad)" == 1 ]] || { echo 'Install only with confirmed parked state'; exit 1; }
[[ -r /data/media/0/realdata && -w /data ]]
available=$(df -Pk /data | awk 'NR==2 {print $4}')
(( available >= 2097152 )) || { echo 'At least 2 GiB free /data storage required'; exit 1; }
echo 'Preflight passed: parked, architecture, systemd, cgroup controllers, FFmpeg/H.264/MCAP filters, permissions, sudo, and free space.'
PREFLIGHT
if [[ "$DASH_MODE" == check ]]; then exit 0; fi
DASH_ARCH="$(ssh "${DASH_SSH_OPTIONS[@]}" "$DASH_TARGET" uname -m)"
case "$DASH_ARCH" in aarch64|arm64) DASH_GOARCH=arm64 ;; x86_64) DASH_GOARCH=amd64 ;; esac
command -v python3 >/dev/null || { echo 'The SSH client requires Python 3.9+ to verify the prebuilt release'; exit 1; }
DASH_STAGING="$(mktemp -d)"
trap 'rm -rf "$DASH_STAGING"' EXIT
if "$DASH_SOURCE"; then
  if [[ -f .artifacts/toolchains/env.sh ]]; then source .artifacts/toolchains/env.sh; fi
  bash scripts/setup-build-env.sh --check
  make check
  DASH_RELEASE_ARCH="$DASH_GOARCH" node scripts/release.mjs --source
  python3 scripts/fetch-release.py --local-manifest .artifacts/release/manifest.json --arch "$DASH_GOARCH" --destination "$DASH_STAGING/payload"
else
  python3 scripts/fetch-release.py --channel "$DASH_CHANNEL" --release "$DASH_RELEASE" --repo "$DASH_REPOSITORY" --arch "$DASH_GOARCH" --destination "$DASH_STAGING/payload"
fi
DASH_TAG="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["tag"])' "$DASH_STAGING/payload/release.json")"
python3 - "$DASH_STAGING/update.json" "$DASH_REPOSITORY" "$DASH_CHANNEL" "$DASH_SOURCE" <<'CONFIG'
import json, sys
with open(sys.argv[1], 'w') as output:
    json.dump({'repository': sys.argv[2], 'channel': sys.argv[3], 'automatic': sys.argv[4] != 'true'}, output)
CONFIG
ssh "${DASH_SSH_OPTIONS[@]}" "$DASH_TARGET" 'set -e; [[ "$(cat /data/params/d/IsOnroad)" == 0 && "$(cat /data/params/d/IsOffroad)" == 1 ]] || exit 1; sudo -n install -d -o root -g comma -m 1770 /data/dash; mkdir -p /data/dash/releases; rm -rf -- /data/dash/.install.new; mkdir -p /data/dash/.install.new'
scp -o BatchMode=yes -o ConnectTimeout=10 -r "$DASH_STAGING/payload/." "$DASH_TARGET:/data/dash/.install.new/"
scp -o BatchMode=yes -o ConnectTimeout=10 dash-backend/install-service.sh "$DASH_TARGET:/data/dash/.install-service.new"
scp -o BatchMode=yes -o ConnectTimeout=10 dash-backend/verify-service.sh "$DASH_TARGET:/data/dash/.verify-service.new"
scp -o BatchMode=yes -o ConnectTimeout=10 dash-backend/systemd/dash.service "$DASH_TARGET:/data/dash/.service.new"
scp -o BatchMode=yes -o ConnectTimeout=10 "$DASH_STAGING/update.json" "$DASH_TARGET:/data/dash/.update.new"
ssh "${DASH_SSH_OPTIONS[@]}" "$DASH_TARGET" bash -s <<'ACTIVATE'
set -euo pipefail
[[ "$(cat /data/params/d/IsOnroad)" == 0 && "$(cat /data/params/d/IsOffroad)" == 1 ]] || { echo 'Device no longer parked; staged files left inactive'; exit 1; }
if systemctl cat dash.service >/dev/null 2>&1; then sudo -n systemctl stop dash.service; fi
DASH_TAG="$(sed -n 's/.*"tag": *"\([a-zA-Z0-9.+_-]*\)".*/\1/p' /data/dash/.install.new/release.json)"
[[ "$DASH_TAG" =~ ^[a-zA-Z0-9][a-zA-Z0-9.+_-]{0,127}$ ]] || { echo 'Invalid staged tag'; exit 1; }
# The bootstrap is stable across OTA. Only an explicit SSH install replaces it.
sudo -n install -o root -g root -m 755 /data/dash/.install.new/dash-backend /data/dash/dash-bootstrap
sudo -n install -o root -g root -m 755 /data/dash/.install-service.new /data/dash/install-service.sh
sudo -n install -o root -g root -m 755 /data/dash/.verify-service.new /data/dash/verify-service.sh
sudo -n install -d -o root -g root -m 755 /data/dash/systemd
sudo -n install -o root -g root -m 644 /data/dash/.service.new /data/dash/systemd/dash.service
if [[ -e "/data/dash/releases/$DASH_TAG" ]]; then
  cmp -s /data/dash/.install.new/release.json "/data/dash/releases/$DASH_TAG/release.json" || { echo 'Existing release identity differs'; exit 1; }
  # Reinstall also repairs an existing build's files, after stopping its process.
  mv "/data/dash/releases/$DASH_TAG" /data/dash/.install.old
  mv /data/dash/.install.new "/data/dash/releases/$DASH_TAG"
  rm -rf /data/dash/.install.old
else
  mv /data/dash/.install.new "/data/dash/releases/$DASH_TAG"
fi
DASH_PREVIOUS="$(readlink /data/dash/current || true)"
ln -s "releases/$DASH_TAG" /data/dash/.current.new
mv -Tf /data/dash/.current.new /data/dash/current
mv /data/dash/.update.new /data/dash/update.json
rm -f /data/dash/pending.json /data/dash/activation.json /data/dash/ready.json
if ! sudo -n bash /data/dash/install-service.sh; then
  sudo -n systemctl disable --now dash.service || true
  if [[ "$DASH_PREVIOUS" =~ ^releases/[a-zA-Z0-9.+_-]+$ ]]; then
    ln -s "$DASH_PREVIOUS" /data/dash/.current.new
    mv -Tf /data/dash/.current.new /data/dash/current
  fi
  echo 'Installation failed; previous release restored when available. Service remains stopped.'
  exit 1
fi
rm -f /data/dash/.install-service.new /data/dash/.verify-service.new /data/dash/.service.new
ACTIVATE
echo "Installed. Open http://${DASH_TARGET#*@}:8088 on the same LAN or device hotspot."
echo 'Lifecycle: sudo systemctl {start|stop|restart|status} dash.service'
