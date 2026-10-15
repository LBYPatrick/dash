#!/bin/bash
set -euo pipefail
systemctl is-active --quiet dash.service
fragment="$(systemctl show dash.service --property=FragmentPath --value)"
source /data/dash/systemd-registration.sh
dash_unit_owned "$fragment"
# Verify the boot-readable registration, not only the loaded in-memory unit.
[[ -f "$fragment" && ! -L "$fragment" ]]
cmp -s "$fragment" /data/dash/systemd/dash.service
[[ -x /data/dash/boot-state.sh && -f /run/dash/boot-validated && ! -L /run/dash/boot-validated && "$(cat /run/dash/boot-validated)" == validated ]]
[[ "$(systemctl show dash.service --property=KillMode --value)" == control-group ]]
[[ "$(systemctl show dash.service --property=Restart --value)" == on-failure ]]
# A fresh marker binds readiness to our running supervisor and child. Do not
# probe global /proc, memory, controller availability, CPU topology or affinity.
ready=/data/dash/cache/supervisor-ready.json
[[ -f "$ready" && "$(wc -c < "$ready")" -le 2048 ]]
marker="$(cat "$ready")"
bootstrap="$(sed -n 's/.*"bootstrap": *\([0-9][0-9]*\).*/\1/p' <<< "$marker")"
child="$(sed -n 's/.*"child": *\([0-9][0-9]*\).*/\1/p' <<< "$marker")"
tag="$(sed -n 's/.*"tag": *"\([a-zA-Z0-9.+_-]*\)".*/\1/p' <<< "$marker")"
[[ "$bootstrap" == "$(systemctl show dash.service --property=MainPID --value)" ]]
[[ "$child" =~ ^[1-9][0-9]*$ ]]
kill -0 "$child"
[[ "$(readlink /data/dash/current)" == "releases/$tag" && ! -e /data/dash/activation.json ]]
/data/dash/current/dash-backend --health-check
/data/dash/current/dash-backend --resource-settings
echo 'Verified Dash readiness, supervised process identity, restart policy and process-group cleanup.'
echo 'CPU uses the fixed app thread budget without quotas or affinity. Memory limits are requested through systemd; unsupported kernel controls are not probed.'
