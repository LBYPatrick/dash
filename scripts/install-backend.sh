#!/bin/bash
set -euo pipefail
DASH_MODE=install
DASH_CHANNEL=main
DASH_RELEASE=''
DASH_REPOSITORY=LBYPatrick/dash
DASH_TARGET=''
DASH_IDENTITY=''
DASH_GOARCH=arm64
DASH_RUNTIME=false
DASH_SOURCE=false
DASH_FEED_OPTION=false
while (( $# )); do
  case "$1" in
    --check) DASH_MODE=check; shift ;;
    --runtime) DASH_RUNTIME=true; shift ;;
    --source) DASH_SOURCE=true; shift ;;
    --identity|--arch)
      if (( $# < 2 )); then echo "Missing value for $1"; exit 2; fi
      case "$1" in --identity) DASH_IDENTITY="$2" ;; --arch) DASH_GOARCH="$2" ;; esac
      shift 2 ;;
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
  echo 'Usage: bash scripts/install-backend.sh [--runtime] [--identity KEY] [--arch arm64|amd64] [--check] [--source | --channel main|nightly|dev] [--release TAG] [--repo OWNER/REPO] comma@DEVICE_IP'
  exit 2
fi
if "$DASH_SOURCE" && "$DASH_FEED_OPTION"; then echo '--source cannot be combined with channel/release/repository options'; exit 2; fi
case "$DASH_CHANNEL" in main|nightly|dev) ;; *) echo 'Invalid channel'; exit 2 ;; esac
if [[ ! "$DASH_REPOSITORY" =~ ^[a-zA-Z0-9_-][a-zA-Z0-9_.-]*/[a-zA-Z0-9_-][a-zA-Z0-9_.-]*$ || ( -n "$DASH_RELEASE" && ! "$DASH_RELEASE" =~ ^[a-zA-Z0-9][a-zA-Z0-9.+_-]{0,127}$ ) ]]; then echo 'Invalid repository or release tag'; exit 2; fi
DASH_PROJECT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$DASH_PROJECT"
DASH_SSH_OPTIONS=(-o BatchMode=yes -o ConnectTimeout=10)
case "$DASH_GOARCH" in arm64|amd64) ;; *) echo 'Unsupported architecture'; exit 2 ;; esac
if [[ -n "$DASH_IDENTITY" ]]; then
  [[ -f "$DASH_IDENTITY" ]] || { echo 'SSH identity file is missing'; exit 2; }
  DASH_SSH_OPTIONS+=(-i "$DASH_IDENTITY" -o IdentitiesOnly=yes)
fi
# This block is intentionally read-only and never changes driving params.
ssh "${DASH_SSH_OPTIONS[@]}" "$DASH_TARGET" bash -s -- "$DASH_RUNTIME" <<'PREFLIGHT'
set -euo pipefail
command -v systemctl >/dev/null
command -v findmnt >/dev/null
command -v mount >/dev/null
id comma >/dev/null
[[ ! -L /data/dash ]] || { echo '/data/dash must not be a symlink'; exit 1; }
# Only inspect our own unit, never replace another application's registration.
fragment="$(systemctl show dash.service --property=FragmentPath --value 2>/dev/null || true)"
[[ -z "$fragment" || "$(readlink -f -- "$fragment")" == /data/dash/systemd/dash.service ]] || { echo 'dash.service belongs to another installation'; exit 1; }
[[ -d /data/media/0/realdata && -d /data/params/d ]]
[[ "$(cat /data/params/d/IsOnroad)" == 0 && "$(cat /data/params/d/IsOffroad)" == 1 ]] || { echo 'Install only with confirmed parked state'; exit 1; }
[[ -r /data/media/0/realdata && -w /data ]]
echo 'Preflight passed: confirmed parked, recording permissions, app folder and systemd. No CPU, memory, controller or topology probes.'
PREFLIGHT
if [[ "$DASH_MODE" == check ]]; then exit 0; fi
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
if "$DASH_RUNTIME"; then printf runtime > "$DASH_STAGING/startup-mode"; else printf persistent > "$DASH_STAGING/startup-mode"; fi
python3 - "$DASH_STAGING/update.json" "$DASH_REPOSITORY" "$DASH_CHANNEL" "$DASH_SOURCE" <<'CONFIG'
import json, sys
with open(sys.argv[1], 'w') as output:
    json.dump({'repository': sys.argv[2], 'channel': sys.argv[3], 'automatic': sys.argv[4] != 'true'}, output)
CONFIG
ssh "${DASH_SSH_OPTIONS[@]}" "$DASH_TARGET" 'set -e; [[ "$(cat /data/params/d/IsOnroad)" == 0 && "$(cat /data/params/d/IsOffroad)" == 1 ]] || exit 1; sudo -n install -d -o root -g comma -m 1770 /data/dash; mkdir -p /data/dash/releases; rm -rf -- /data/dash/.install.new; mkdir -p /data/dash/.install.new'
scp "${DASH_SSH_OPTIONS[@]}" -r "$DASH_STAGING/payload/." "$DASH_TARGET:/data/dash/.install.new/"
scp "${DASH_SSH_OPTIONS[@]}" dash-backend/install-service.sh "$DASH_TARGET:/data/dash/.install-service.new"
scp "${DASH_SSH_OPTIONS[@]}" dash-backend/systemd-registration.sh "$DASH_TARGET:/data/dash/.registration.new"
scp "${DASH_SSH_OPTIONS[@]}" dash-backend/uninstall-service.sh "$DASH_TARGET:/data/dash/.uninstall-service.new"
scp "${DASH_SSH_OPTIONS[@]}" dash-backend/verify-service.sh "$DASH_TARGET:/data/dash/.verify-service.new"
scp "${DASH_SSH_OPTIONS[@]}" dash-backend/systemd/dash.service "$DASH_TARGET:/data/dash/.service.new"
scp "${DASH_SSH_OPTIONS[@]}" "$DASH_STAGING/startup-mode" "$DASH_TARGET:/data/dash/.startup.new"
scp "${DASH_SSH_OPTIONS[@]}" "$DASH_STAGING/update.json" "$DASH_TARGET:/data/dash/.update.new"
ssh "${DASH_SSH_OPTIONS[@]}" "$DASH_TARGET" bash -s <<'ACTIVATE'
set -euo pipefail
[[ "$(cat /data/params/d/IsOnroad)" == 0 && "$(cat /data/params/d/IsOffroad)" == 1 ]] || { echo 'Device no longer parked; staged files left inactive'; exit 1; }
# Replace durable boot files only after their complete contents are on storage.
# GNU sync with a path fsyncs that file/directory, not the entire filesystem.
dash_install_atomic() {
  local source="$1" destination="$2" mode="$3"
  sudo -n install -o root -g root -m "$mode" "$source" "$destination.next"
  sudo -n sync -- "$destination.next"
  sudo -n mv -Tf -- "$destination.next" "$destination"
  sudo -n sync -- "$(dirname "$destination")"
}
DASH_PREVIOUS="$(readlink /data/dash/current || true)"
dash_activation_failed() {
  trap - ERR
  sudo -n systemctl stop dash.service || true
  if [[ "$DASH_PREVIOUS" =~ ^releases/[a-zA-Z0-9.+_-]+$ ]]; then
    ln -sfn "$DASH_PREVIOUS" /data/dash/.current.new
    mv -Tf /data/dash/.current.new /data/dash/current
    sync -- /data/dash
  fi
  echo 'Activation failed; previous release restored when available. Dash remains stopped.' >&2
  exit 1
}
DASH_TAG="$(sed -n 's/.*"tag": *"\([a-zA-Z0-9.+_-]*\)".*/\1/p' /data/dash/.install.new/release.json)"
[[ "$DASH_TAG" =~ ^[a-zA-Z0-9][a-zA-Z0-9.+_-]{0,127}$ ]] || { echo 'Invalid staged tag'; exit 1; }
# Never remove a release that the boot symlink might still reference. An
# identical reinstall can reuse it; repair a damaged release with a fresh tag.
if [[ -e "/data/dash/releases/$DASH_TAG" ]]; then
  diff -qr /data/dash/.install.new "/data/dash/releases/$DASH_TAG" >/dev/null || { echo 'Existing release differs; install a fresh --source build or another release tag'; exit 1; }
fi
# Flush staged payloads before publishing their directory or boot pointer.
find /data/dash/.install.new -type f -exec sync -- {} +
find /data/dash/.install.new -depth -type d -exec sync -- {} +
trap dash_activation_failed ERR
if systemctl cat dash.service >/dev/null 2>&1; then sudo -n systemctl stop dash.service; fi
# The bootstrap is stable across OTA. Only an explicit SSH install replaces it.
dash_install_atomic /data/dash/.install.new/dash-backend /data/dash/dash-bootstrap 755
dash_install_atomic /data/dash/.install-service.new /data/dash/install.sh 755
dash_install_atomic /data/dash/.registration.new /data/dash/systemd-registration.sh 755
dash_install_atomic /data/dash/.uninstall-service.new /data/dash/uninstall.sh 755
dash_install_atomic /data/dash/.verify-service.new /data/dash/verify-service.sh 755
sudo -n install -d -o root -g root -m 755 /data/dash/systemd
dash_install_atomic /data/dash/.service.new /data/dash/systemd/dash.service 644
if [[ -e "/data/dash/releases/$DASH_TAG" ]]; then
  rm -rf /data/dash/.install.new
else
  mv /data/dash/.install.new "/data/dash/releases/$DASH_TAG"
fi
sync -- /data/dash/releases
ln -sfn "releases/$DASH_TAG" /data/dash/.current.new
mv -Tf /data/dash/.current.new /data/dash/current
sync -- /data/dash
sync -- /data/dash/.update.new
mv /data/dash/.update.new /data/dash/update.json
sync -- /data/dash
rm -f /data/dash/pending.json /data/dash/activation.json /data/dash/ready.json
dash_install_atomic /data/dash/.startup.new /data/dash/startup-mode 644
rm -f /data/dash/.startup.new
DASH_INSTALL_FLAGS=()
if [[ "$(cat /data/dash/startup-mode)" == runtime ]]; then DASH_INSTALL_FLAGS=(--runtime); fi
if ! sudo -n bash /data/dash/install.sh "${DASH_INSTALL_FLAGS[@]}"; then
  sudo -n systemctl stop dash.service || true
  if [[ "$DASH_PREVIOUS" =~ ^releases/[a-zA-Z0-9.+_-]+$ ]]; then
    ln -s "$DASH_PREVIOUS" /data/dash/.current.new
    mv -Tf /data/dash/.current.new /data/dash/current
    sync -- /data/dash
  fi
  echo 'Installation failed; previous release restored when available. Service remains stopped.'
  exit 1
fi
trap - ERR
rm -f /data/dash/.registration.new /data/dash/.install-service.new /data/dash/.verify-service.new /data/dash/.uninstall-service.new /data/dash/.service.new
ACTIVATE
echo "Installed. Open http://${DASH_TARGET#*@}:8088 on the same LAN or device hotspot."
echo 'Lifecycle: sudo systemctl {start|stop|restart|status} dash.service'
