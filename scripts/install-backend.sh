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
ssh "${DASH_SSH_OPTIONS[@]}" "$DASH_TARGET" bash -s -- "$DASH_RUNTIME" < backend/preflight-install.sh
if [[ "$DASH_MODE" == check ]]; then exit 0; fi
command -v go >/dev/null || { echo 'The SSH client requires Go matching backend/go.mod'; exit 1; }
DASH_STAGING="$(mktemp -d)"
trap 'rm -rf "$DASH_STAGING"' EXIT
if "$DASH_SOURCE"; then
  if [[ -f .artifacts/toolchains/env.sh ]]; then source .artifacts/toolchains/env.sh; fi
  bash scripts/setup-build-env.sh --check
  make check
  DASH_RELEASE_ARCH="$DASH_GOARCH" go -C backend run ./cmd/dash-tool release --source
  DASH_TAG="$(go -C backend run ./cmd/dash-tool fetch-release --local-manifest "$DASH_PROJECT/.artifacts/release/manifest.json" --arch "$DASH_GOARCH" --destination "$DASH_STAGING/payload")"
else
  DASH_TAG="$(go -C backend run ./cmd/dash-tool fetch-release --channel "$DASH_CHANNEL" --release "$DASH_RELEASE" --repo "$DASH_REPOSITORY" --arch "$DASH_GOARCH" --destination "$DASH_STAGING/payload")"
fi
if "$DASH_RUNTIME"; then printf runtime > "$DASH_STAGING/startup-mode"; else printf persistent > "$DASH_STAGING/startup-mode"; fi
go -C backend run ./cmd/dash-tool update-config "$DASH_STAGING/update.json" "$DASH_REPOSITORY" "$DASH_CHANNEL" "$DASH_SOURCE"
ssh "${DASH_SSH_OPTIONS[@]}" "$DASH_TARGET" 'set -e; [[ "$(cat /data/params/d/IsOffroad)" == 1 && ( ( ! -e /data/params/d/IsOnroad && ! -L /data/params/d/IsOnroad ) || "$(cat /data/params/d/IsOnroad)" == 0 ) ]] || exit 1; sudo -n install -d -o root -g comma -m 1770 /data/dash; mkdir -p /data/dash/releases; rm -rf -- /data/dash/.install.new; mkdir -p /data/dash/.install.new'
scp "${DASH_SSH_OPTIONS[@]}" -r "$DASH_STAGING/payload/." "$DASH_TARGET:/data/dash/.install.new/"
scp "${DASH_SSH_OPTIONS[@]}" backend/install-service.sh "$DASH_TARGET:/data/dash/.install-service.new"
scp "${DASH_SSH_OPTIONS[@]}" backend/systemd-registration.sh "$DASH_TARGET:/data/dash/.registration.new"
scp "${DASH_SSH_OPTIONS[@]}" backend/uninstall-service.sh "$DASH_TARGET:/data/dash/.uninstall-service.new"
scp "${DASH_SSH_OPTIONS[@]}" backend/boot-state.sh "$DASH_TARGET:/data/dash/.boot-state.new"
scp "${DASH_SSH_OPTIONS[@]}" backend/verify-service.sh "$DASH_TARGET:/data/dash/.verify-service.new"
scp "${DASH_SSH_OPTIONS[@]}" backend/systemd/dash.service "$DASH_TARGET:/data/dash/.service.new"
scp "${DASH_SSH_OPTIONS[@]}" "$DASH_STAGING/startup-mode" "$DASH_TARGET:/data/dash/.startup.new"
scp "${DASH_SSH_OPTIONS[@]}" "$DASH_STAGING/update.json" "$DASH_TARGET:/data/dash/.update.new"
ssh "${DASH_SSH_OPTIONS[@]}" "$DASH_TARGET" bash -s < backend/activate-install.sh
echo "Installed. Open http://${DASH_TARGET#*@}:8088 on the same LAN or device hotspot."
echo 'Lifecycle: sudo systemctl {start|stop|restart|status} dash.service'
