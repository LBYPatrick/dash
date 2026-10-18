#!/bin/bash
set -euo pipefail
systemctl is-active --quiet dash.service
fragment="$(systemctl show dash.service --property=FragmentPath --value)"
source /data/dash/systemd-registration.sh
dash_unit_owned "$fragment"
# Verify the boot-readable registration, not only the loaded in-memory unit.
[[ -f "$fragment" && ! -L "$fragment" ]] || exit 1
cmp -s "$fragment" /data/dash/systemd/dash.service
systemctl is-enabled --quiet dash.service
wants="$(dirname "$fragment")/multi-user.target.wants/dash.service"
[[ -L "$wants" && "$(readlink -f -- "$wants")" == "$fragment" ]] || exit 1
[[ -x /data/dash/boot-state.sh && -f /run/dash/boot-validated && ! -L /run/dash/boot-validated && "$(cat /run/dash/boot-validated)" == validated ]] || exit 1
[[ "$(systemctl show dash.service --property=KillMode --value)" == control-group ]] || exit 1
[[ "$(systemctl show dash.service --property=Restart --value)" == on-failure ]] || exit 1
# A fresh marker binds readiness to our running supervisor and child. Do not
# probe global /proc, memory, controller availability, CPU topology or affinity.
ready=/data/dash/cache/supervisor-ready.json
[[ -f "$ready" && "$(wc -c < "$ready")" -le 2048 ]] || exit 1
marker="$(cat "$ready")"
bootstrap="$(sed -n 's/.*"bootstrap": *\([0-9][0-9]*\).*/\1/p' <<< "$marker")"
child="$(sed -n 's/.*"child": *\([0-9][0-9]*\).*/\1/p' <<< "$marker")"
tag="$(sed -n 's/.*"tag": *"\([a-zA-Z0-9.+_-]*\)".*/\1/p' <<< "$marker")"
[[ "$bootstrap" == "$(systemctl show dash.service --property=MainPID --value)" ]] || exit 1
[[ "$child" =~ ^[1-9][0-9]*$ ]] || exit 1
kill -0 "$child"
[[ "$(readlink /data/dash/current)" == "releases/$tag" && ! -e /data/dash/activation.json ]] || exit 1
tls_args=()
if [[ -f /data/dash/dash.env ]]; then
  # Read only the documented systemd TLS assignment. Never source/evaluate an
  # environment file as shell code, or include unrelated destination secrets.
  while IFS= read -r line || [[ -n "$line" ]]; do
    if [[ "$line" == DASH_TLS_ARGS=* ]]; then
      tls_value="${line#DASH_TLS_ARGS=}"
      tls_value="${tls_value#\"}"; tls_value="${tls_value%\"}"
      tls_value="${tls_value#\'}"; tls_value="${tls_value%\'}"
      read -r -a tls_args <<< "$tls_value"
    fi
  done < /data/dash/dash.env
fi
/data/dash/current/dash-backend --health-check ${tls_args[@]+"${tls_args[@]}"}
/data/dash/current/dash-backend --resource-settings
if [[ -f /data/dash/systemd/dash-usb.service ]]; then
  systemctl is-active --quiet dash-usb.service
  systemctl is-enabled --quiet dash-usb.service
  helper="$(systemctl show dash-usb.service --property=FragmentPath --value)"
  [[ -f "$helper" && ! -L "$helper" ]]
  cmp -s "$helper" /data/dash/systemd/dash-usb.service
  [[ "$(readlink -f -- "$(dirname "$helper")/multi-user.target.wants/dash-usb.service")" == "$helper" ]]
  [[ -S /run/dash-usb/control.sock ]]
fi
echo 'Verified Dash readiness, boot-readable unit and enablement, supervised process identity, restart policy and process-group cleanup.'
echo 'CPU uses the fixed app thread budget without quotas or affinity. Memory limits are requested through systemd; unsupported kernel controls are not probed.'
