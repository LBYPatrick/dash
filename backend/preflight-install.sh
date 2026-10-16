#!/bin/bash
set -euo pipefail
command -v systemctl >/dev/null
command -v findmnt >/dev/null
command -v mount >/dev/null
id comma >/dev/null
[[ ! -L /data/dash ]] || { echo '/data/dash must not be a symlink'; exit 1; }
# Only inspect our own unit, never replace another application's registration.
fragment="$(systemctl show dash.service --property=FragmentPath --value 2>/dev/null || true)"
if [[ -n "$fragment" && "$(readlink -f -- "$fragment")" != /data/dash/systemd/dash.service ]]; then
  [[ ( "$fragment" == /etc/systemd/system/dash.service || "$fragment" == /run/systemd/system/dash.service ) && -f "$fragment" && ! -L "$fragment" && "$(head -n 1 "$fragment")" == '# Dash managed service. Source: /data/dash/systemd/dash.service' ]] || { echo 'dash.service belongs to another installation'; exit 1; }
fi
# Reject this mode mismatch before downloads, uploads or stopping the service.
if [[ "$1" == true ]] && [[ -e /etc/systemd/system/dash.service || -L /etc/systemd/system/dash.service || -e /etc/systemd/system/multi-user.target.wants/dash.service || -L /etc/systemd/system/multi-user.target.wants/dash.service ]]; then
  echo 'Persistent Dash registration exists; use persistent mode or uninstall before a runtime-only installation.' >&2
  exit 1
fi
[[ -d /data/media/0/realdata && -d /data/params/d ]]
[[ "$(cat /data/params/d/IsOffroad)" == 1 && ( ( ! -e /data/params/d/IsOnroad && ! -L /data/params/d/IsOnroad ) || "$(cat /data/params/d/IsOnroad)" == 0 ) ]] || { echo 'Install only with confirmed parked state'; exit 1; }
[[ -r /data/media/0/realdata && -w /data ]]
for directory in /etc/systemd/system /run/systemd/system; do
  file="$directory/dash-usb.service"
  if [[ -e "$file" || -L "$file" ]]; then
    [[ -f "$file" && ! -L "$file" && "$(head -n 1 "$file")" == '# Dash managed USB service. Source: /data/dash/systemd/dash-usb.service' ]] || { echo 'dash-usb.service belongs to another installation'; exit 1; }
  fi
  link="$directory/multi-user.target.wants/dash-usb.service"
  if [[ -e "$link" || -L "$link" ]]; then
    [[ -L "$link" && ( "$(readlink "$link")" == ../dash-usb.service || "$(readlink "$link")" == "$file" ) ]] || { echo 'Foreign USB helper enablement'; exit 1; }
  fi
done
if [[ "$1" == true && ( -e /etc/systemd/system/dash-usb.service || -L /etc/systemd/system/dash-usb.service || -e /etc/systemd/system/multi-user.target.wants/dash-usb.service || -L /etc/systemd/system/multi-user.target.wants/dash-usb.service ) ]]; then
  echo 'Persistent USB helper registration exists; keep persistent mode or uninstall first.'; exit 1
fi
echo 'Preflight passed: confirmed parked, recording permissions, app folder and systemd. No CPU, memory, controller or topology probes.'
