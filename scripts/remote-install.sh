#!/bin/bash
# Public entry point: curl -fsSL …/scripts/remote-install.sh | bash
set -euo pipefail
channel=main
repository=LBYPatrick/dash
release=''
runtime=false
target=''
identity=''
while (( $# )); do
  case "$1" in
    --channel|--repo|--release|--target|--identity)
      [[ $# -ge 2 && -n "$2" ]] || { echo "$1 requires a value" >&2; exit 2; }
      case "$1" in
        --channel) channel="$2" ;; --repo) repository="$2" ;; --release) release="$2" ;;
        --target) target="$2" ;; --identity) identity="$2" ;;
      esac
      shift 2 ;;
    --runtime) runtime=true; shift ;;
    --help)
      echo 'Usage: remote-install.sh [--channel main|nightly|dev] [--release TAG] [--runtime] [--repo OWNER/REPO] [--target comma@HOST] [--identity KEY]'
      exit 0 ;;
    *) echo "Unknown option: $1" >&2; exit 2 ;;
  esac
done
case "$channel" in main|nightly|dev) ;; *) echo 'Invalid channel' >&2; exit 2 ;; esac
[[ "$repository" =~ ^[a-zA-Z0-9_-][a-zA-Z0-9_.-]*/[a-zA-Z0-9_-][a-zA-Z0-9_.-]*$ ]] || { echo 'Invalid repository' >&2; exit 2; }
[[ -z "$release" || "$release" =~ ^[a-zA-Z0-9][a-zA-Z0-9.+_-]{0,127}$ ]] || { echo 'Invalid release tag' >&2; exit 2; }
args=(--channel "$channel" --repo "$repository")
[[ -z "$release" ]] || args+=(--release "$release")
if "$runtime"; then args+=(--runtime); fi
if [[ -n "$target" ]]; then
  [[ "$target" =~ ^comma@[a-zA-Z0-9._-]+$ ]] || { echo 'Use --target comma@HOST' >&2; exit 2; }
  ssh_options=(-o BatchMode=yes -o ConnectTimeout=10)
  if [[ -n "$identity" ]]; then
    [[ -f "$identity" ]] || { echo 'SSH identity file is missing' >&2; exit 2; }
    ssh_options+=(-i "$identity" -o IdentitiesOnly=yes)
  fi
  # Fetch a complete script before execution, including when stdin is a pipe.
  printf -v remote_args ' %q' "${args[@]}"
  ssh "${ssh_options[@]}" "$target" "set -e; t=\$(mktemp); trap 'rm -f \"\$t\"' EXIT; curl --proto '=https' --tlsv1.2 -fsSL --retry 3 --connect-timeout 15 --max-time 60 --max-filesize 65536 'https://raw.githubusercontent.com/$repository/main/scripts/remote-install.sh' -o \"\$t\"; bash \"\$t\"$remote_args"
  printf 'Open http://%s:8088 on the same network.\n' "${target#*@}"
  exit 0
fi
[[ -z "$identity" ]] || { echo '--identity requires --target' >&2; exit 2; }
[[ "$(uname -s)" == Linux && "$(id -un)" == comma ]] || { echo 'Run as comma on the device, or pass --target comma@HOST from your computer.' >&2; exit 1; }
[[ ! -L /data/dash && -d /data/media/0/realdata ]] || { echo 'Unsupported device layout' >&2; exit 1; }
[[ "$(cat /data/params/d/IsOffroad)" == 1 && ( ( ! -e /data/params/d/IsOnroad && ! -L /data/params/d/IsOnroad ) || "$(cat /data/params/d/IsOnroad)" == 0 ) ]] || { echo 'Install only with confirmed parked state' >&2; exit 1; }
locator="${release:-channel-$channel}"
temporary="$(mktemp -d)"
trap 'rm -rf "$temporary"' EXIT
echo "Fetching the Dash $channel installer…"
curl --proto '=https' --proto-redir '=https' --tlsv1.2 -fsSL --retry 3 --connect-timeout 15 --max-time 60 --max-filesize 65536 \
  "https://github.com/$repository/releases/download/$locator/bootstrap.sh" -o "$temporary/bootstrap.sh"
bash "$temporary/bootstrap.sh" "${args[@]}"
