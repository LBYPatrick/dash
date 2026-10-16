#!/bin/bash
# Generated for one immutable release. Downloaded by remote-install.sh.
set -euo pipefail
repository=LBYPatrick/dash
channel='__CHANNEL__'
tag='__TAG__'
runtime=false
while (( $# )); do
  case "$1" in
    --repo|--channel|--release)
      [[ $# -ge 2 && -n "$2" ]] || exit 2
      case "$1" in
        --repo) repository="$2" ;;
        --channel) [[ "$2" == "$channel" ]] || { echo 'Installer channel mismatch' >&2; exit 1; } ;;
        --release) [[ "$2" == "$tag" ]] || { echo 'Installer tag mismatch' >&2; exit 1; } ;;
      esac
      shift 2 ;;
    --runtime) runtime=true; shift ;;
    *) echo "Unknown option: $1" >&2; exit 2 ;;
  esac
done
[[ "$repository" =~ ^[a-zA-Z0-9_-][a-zA-Z0-9_.-]*/[a-zA-Z0-9_-][a-zA-Z0-9_.-]*$ ]] || exit 2
[[ "$(uname -s)" == Linux && "$(id -un)" == comma ]] || { echo 'Run as comma on the device, without sudo.' >&2; exit 1; }
[[ "$(cat /data/params/d/IsOffroad)" == 1 && ( ( ! -e /data/params/d/IsOnroad && ! -L /data/params/d/IsOnroad ) || "$(cat /data/params/d/IsOnroad)" == 0 ) ]] || { echo 'Install only with confirmed parked state' >&2; exit 1; }
case "$(uname -m)" in
  aarch64|arm64) arch=arm64; checksum='__ARM64_SHA__' ;;
  x86_64) arch=amd64; checksum='__AMD64_SHA__' ;;
  *) echo 'Unsupported device architecture' >&2; exit 1 ;;
esac
[[ "$checksum" =~ ^[a-f0-9]{64}$ ]] || { echo 'This release has no installer for the device' >&2; exit 1; }
temporary="$(mktemp -d)"
trap 'rm -rf "$temporary"' EXIT
name="dash-bootstrap-linux-$arch"
echo "Downloading $tag installer…"
curl --proto '=https' --proto-redir '=https' --tlsv1.2 -fsSL --retry 3 --connect-timeout 15 --max-time 300 --max-filesize 33554432 \
  "https://github.com/$repository/releases/download/$tag/$name" -o "$temporary/$name"
[[ "$(cat /data/params/d/IsOffroad)" == 1 && ( ( ! -e /data/params/d/IsOnroad && ! -L /data/params/d/IsOnroad ) || "$(cat /data/params/d/IsOnroad)" == 0 ) ]] || { echo 'Device no longer parked; installation stopped' >&2; exit 1; }
(cd "$temporary"; printf '%s  %s\n' "$checksum" "$name" | sha256sum -c - >/dev/null)
chmod 700 "$temporary/$name"
args=(--install-release --install-repo "$repository" --install-channel "$channel" --install-tag "$tag")
if "$runtime"; then args+=(--install-runtime); fi
"$temporary/$name" "${args[@]}"
