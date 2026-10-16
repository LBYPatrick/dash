#!/bin/bash
set -euo pipefail
options=()
case "${1:-}" in --runtime) options=(--runtime) ;; '') ;; *) echo 'Usage: install.sh [--runtime]'; exit 2 ;; esac
[[ $# -le 1 ]]
[[ "$(id -u)" == 0 ]] || { echo 'Run: sudo bash /data/dash/install.sh'; exit 1; }
[[ -d /data/dash && ! -L /data/dash ]]
[[ "$(cat /data/params/d/IsOffroad)" == 1 && ( ( ! -e /data/params/d/IsOnroad && ! -L /data/params/d/IsOnroad ) || "$(cat /data/params/d/IsOnroad)" == 0 ) ]] || { echo 'Installation requires confirmed parked state'; exit 1; }
source /data/dash/systemd-registration.sh
dash_validate_registrations
if [[ ${#options[@]} != 0 ]]; then dash_runtime_install_allowed; fi
fragment="$(systemctl show dash.service --property=FragmentPath --value 2>/dev/null || true)"
[[ -z "$fragment" ]] || dash_unit_owned "$fragment" || { echo 'Refusing to replace another dash.service'; exit 1; }
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
usb_high="$(sed -n 's/.*"usbMemoryHighMiB":\([0-9][0-9]*\).*/\1/p' <<< "$settings")"
usb_max="$(sed -n 's/.*"usbMemoryMaxMiB":\([0-9][0-9]*\).*/\1/p' <<< "$settings")"
[[ "$usb_high" =~ ^[1-9][0-9]*$ && "$usb_max" =~ ^[1-9][0-9]*$ && "$usb_max" -ge "$usb_high" ]]
sed -i "s/^MemoryHigh=.*/MemoryHigh=${usb_high}M/; s/^MemoryMax=.*/MemoryMax=${usb_max}M/" /data/dash/systemd/dash-usb.service
sync -- /data/dash/systemd/dash.service /data/dash/systemd
install -d -o comma -g comma -m 700 /data/dash/cache
for directory in /data/dash/usb /data/dash/usb-tools; do
  [[ ! -L "$directory" ]] || { echo 'Refusing unsafe USB service directory'; exit 1; }
  install -d -o root -g root -m 755 "$directory"
done
if [[ -f /data/dash/current/bin/usb/manifest.json ]]; then
  /data/dash/current/dash-backend --verify-usb-bundle
  for tool in mount.exfat-fuse ntfs-3g; do
    install -o root -g root -m 755 "/data/dash/current/bin/usb/$tool" "/data/dash/usb-tools/$tool.next"
    sync -- "/data/dash/usb-tools/$tool.next"
    mv -Tf "/data/dash/usb-tools/$tool.next" "/data/dash/usb-tools/$tool"
  done
  for category in sources licenses; do
    [[ -d "/data/dash/current/bin/usb/$category" && ! -L "/data/dash/current/bin/usb/$category" ]] || { echo 'USB helper sources/licenses are missing'; exit 1; }
    cp -R --no-dereference "/data/dash/current/bin/usb/$category" /data/dash/usb-tools/
    chown -R root:root "/data/dash/usb-tools/$category"
  done
  sync -- /data/dash/usb-tools
fi
runtime=false
if [[ ${#options[@]} != 0 ]]; then runtime=true; fi
dash_systemd_registration register "$runtime"
systemctl daemon-reload
systemctl reset-failed dash.service
systemctl reset-failed dash-usb.service 2>/dev/null || true
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
  printf persistent > /data/dash/.startup-mode.next
  echo 'Dash enabled at boot.'
else
  printf runtime > /data/dash/.startup-mode.next
  echo 'Dash registered for this boot only; runtime links disappear after reboot.'
fi
sync -- /data/dash/.startup-mode.next
mv -Tf /data/dash/.startup-mode.next /data/dash/startup-mode
sync -- /data/dash
echo 'Logs: journalctl -u dash.service -f'
