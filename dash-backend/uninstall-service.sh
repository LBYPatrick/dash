#!/bin/bash
set -euo pipefail
[[ $# == 0 ]] || { echo 'Usage: sudo bash /data/dash/uninstall.sh'; exit 2; }
[[ "$(id -u)" == 0 ]] || { echo 'Run: sudo bash /data/dash/uninstall.sh'; exit 1; }
[[ -d /data/dash && ! -L /data/dash ]] || { echo 'Unsafe or missing Dash directory'; exit 1; }
[[ "$(cat /data/params/d/IsOnroad)" == 0 && "$(cat /data/params/d/IsOffroad)" == 1 ]] || { echo 'Uninstall requires confirmed parked state'; exit 1; }
fragment="$(systemctl show dash.service --property=FragmentPath --value 2>/dev/null || true)"
[[ -z "$fragment" || "$(readlink -f -- "$fragment")" == /data/dash/systemd/dash.service ]] || { echo 'Refusing to touch another dash.service'; exit 1; }
options=()
if [[ -f /data/dash/startup-mode && "$(cat /data/dash/startup-mode)" == runtime ]]; then options=(--runtime); fi
source /data/dash/systemd-registration.sh
runtime=false
if [[ ${#options[@]} != 0 ]]; then runtime=true; fi
if [[ -n "$fragment" ]]; then
  systemctl stop dash.service
  dash_systemd_registration remove "$runtime"
fi
systemctl daemon-reload
systemctl reset-failed dash.service 2>/dev/null || true
# No firmware, parameters, recordings or system packages are removed. Running
# this loaded script remains safe after its own installation directory is gone.
cd /
rm -rf --one-file-system -- /data/dash
echo 'Dash removed. Original recordings and firmware are untouched.'
