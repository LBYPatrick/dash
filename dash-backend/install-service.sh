#!/bin/bash
set -euo pipefail
options=()
case "${1:-}" in --runtime) options=(--runtime) ;; '') ;; *) echo 'Usage: install.sh [--runtime]'; exit 2 ;; esac
[[ $# -le 1 ]]
[[ "$(id -u)" == 0 ]] || { echo 'Run: sudo bash /data/dash/install.sh'; exit 1; }
[[ -d /data/dash && ! -L /data/dash ]]
[[ "$(cat /data/params/d/IsOnroad)" == 0 && "$(cat /data/params/d/IsOffroad)" == 1 ]] || { echo 'Installation requires confirmed parked state'; exit 1; }
fragment="$(systemctl show dash.service --property=FragmentPath --value 2>/dev/null || true)"
[[ -z "$fragment" || "$(readlink -f -- "$fragment")" == /data/dash/systemd/dash.service ]] || { echo 'Refusing to replace another dash.service'; exit 1; }
release="$(readlink /data/dash/current)"
[[ "$release" =~ ^releases/[a-zA-Z0-9.+_-]+$ && ! -L "/data/dash/$release" ]]
for tool in ffmpeg ffprobe; do
  [[ -f "/data/dash/current/bin/$tool" && ! -L "/data/dash/current/bin/$tool" && -x "/data/dash/current/bin/$tool" ]] || { echo "Bundled $tool missing"; exit 1; }
done
# Read constants from our own binary; never discover device topology/controllers.
settings="$(/data/dash/current/dash-backend --resource-settings)"
high="$(sed -n 's/.*"memoryHighMiB":\([0-9][0-9]*\).*/\1/p' <<< "$settings")"
max="$(sed -n 's/.*"memoryMaxMiB":\([0-9][0-9]*\).*/\1/p' <<< "$settings")"
[[ "$high" =~ ^[1-9][0-9]*$ && "$max" =~ ^[1-9][0-9]*$ && "$max" -ge "$high" ]]
sed -i "s/^MemoryHigh=.*/MemoryHigh=${high}M/; s/^MemoryMax=.*/MemoryMax=${max}M/" /data/dash/systemd/dash.service
install -d -o comma -g comma -m 700 /data/dash/cache
source /data/dash/systemd-registration.sh
runtime=false
if [[ ${#options[@]} != 0 ]]; then runtime=true; fi
dash_systemd_registration register "$runtime"
systemctl daemon-reload
systemctl reset-failed dash.service
if ! systemctl restart dash.service; then
  systemctl stop dash.service
  dash_systemd_registration remove "$runtime"
  journalctl -u dash.service -n 30 --no-pager
  exit 1
fi
healthy=false
for attempt in {1..55}; do
  if bash /data/dash/verify-service.sh >/dev/null 2>&1; then healthy=true; break; fi
  if ! systemctl is-active --quiet dash.service; then break; fi
  sleep 1
done
if ! "$healthy" || ! bash /data/dash/verify-service.sh; then
  systemctl stop dash.service
  dash_systemd_registration remove "$runtime"
  echo 'Startup verification failed; Dash stopped and disabled.'
  journalctl -u dash.service -n 30 --no-pager
  exit 1
fi
if [[ ${#options[@]} == 0 ]]; then
  echo persistent > /data/dash/startup-mode
  echo 'Dash enabled at boot.'
else
  echo runtime > /data/dash/startup-mode
  echo 'Dash registered for this boot only; runtime links disappear after reboot.'
fi
echo 'Logs: journalctl -u dash.service -f'
