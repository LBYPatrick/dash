#!/bin/bash
# Host only: never run this script on the driving device.
set -euo pipefail
DASH_PROJECT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$DASH_PROJECT"
DASH_CHECK=false
case "${1:-}" in --check) DASH_CHECK=true ;; '') ;; *) echo 'Usage: bash scripts/setup-build-env.sh [--check]'; exit 2 ;; esac
if [[ -d /data/params/d && -d /data/media/0/realdata ]]; then
  echo 'Build tools belong on a separate Linux/macOS host, never the driving device.'; exit 1
fi
case "$(uname -s)" in
  Darwin)
    if ! "$DASH_CHECK"; then
      xcode-select -p >/dev/null 2>&1 || { echo 'Install Apple Command Line Tools (xcode-select --install), then rerun.'; exit 1; }
      if ! command -v python3 >/dev/null || ! command -v ffmpeg >/dev/null; then
        command -v brew >/dev/null || { echo 'Install Homebrew from https://brew.sh, then rerun.'; exit 1; }
        brew install python ffmpeg
      fi
    fi ;;
  Linux)
    if ! "$DASH_CHECK"; then
      command -v apt-get >/dev/null || { echo 'Automated system packages support Debian/Ubuntu. On other Linux distributions install Python 3.9+, git, make, C compiler, SSH, CA certificates and FFmpeg 5+, then use --check.'; exit 1; }
      DASH_PRIVILEGE=()
      if (( EUID != 0 )); then DASH_PRIVILEGE=(sudo); fi
      "${DASH_PRIVILEGE[@]}" env DEBIAN_FRONTEND=noninteractive apt-get update
      "${DASH_PRIVILEGE[@]}" env DEBIAN_FRONTEND=noninteractive apt-get install -y ca-certificates python3 git make build-essential openssh-client ffmpeg
    fi ;;
  *) echo 'Use a Linux or macOS build host'; exit 1 ;;
esac
for DASH_TOOL in python3 git make cc ssh scp ffmpeg ffprobe tar; do
  command -v "$DASH_TOOL" >/dev/null || { echo "Missing host tool: $DASH_TOOL"; exit 1; }
done
python3 -c 'import sys; assert sys.version_info >= (3,9), "Python 3.9+ required"'
ffmpeg -hide_banner -h full 2>/dev/null | grep -F -- '-fps_mode' >/dev/null || { echo 'FFmpeg 5+ required'; exit 1; }
if "$DASH_CHECK"; then
  if [[ -f .artifacts/toolchains/env.sh ]]; then source .artifacts/toolchains/env.sh; fi
  python3 scripts/setup-toolchains.py --check
else
  python3 scripts/setup-toolchains.py
  source .artifacts/toolchains/env.sh
  make install
fi
echo 'Build host ready.'
if [[ -f .artifacts/toolchains/env.sh ]]; then echo 'For a new shell: source .artifacts/toolchains/env.sh'; fi
