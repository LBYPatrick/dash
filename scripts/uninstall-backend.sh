#!/bin/bash
set -euo pipefail
options=(-o BatchMode=yes -o ConnectTimeout=10 -o ServerAliveInterval=5 -o ServerAliveCountMax=3)
if [[ "${1:-}" == --identity ]]; then
  [[ $# -ge 3 && -f "$2" ]] || { echo 'Missing SSH identity or target'; exit 2; }
  options+=(-i "$2" -o IdentitiesOnly=yes)
  shift 2
fi
[[ $# == 1 && "$1" =~ ^[a-zA-Z0-9._-]+@[a-zA-Z0-9._-]+$ ]] || { echo 'Usage: bash scripts/uninstall-backend.sh [--identity KEY] comma@DEVICE_IP'; exit 2; }
ssh "${options[@]}" "$1" 'set -e; test ! -L /data/dash; test -f /data/dash/uninstall.sh; sudo -n bash /data/dash/uninstall.sh'
