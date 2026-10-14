#!/bin/bash
set -euo pipefail
systemctl is-active --quiet dash.service
DASH_CGROUP="$(systemctl show dash.service --property=ControlGroup --value)"
DASH_ROOT="/sys/fs/cgroup${DASH_CGROUP}"
[[ "$DASH_CGROUP" == */dash.service ]]
read -r DASH_QUOTA DASH_PERIOD < "$DASH_ROOT/cpu.max"
[[ "$DASH_QUOTA" =~ ^[0-9]+$ && "$DASH_PERIOD" =~ ^[0-9]+$ ]]
(( DASH_QUOTA > 0 && DASH_PERIOD > 0 && DASH_QUOTA <= DASH_PERIOD / 2 ))
for DASH_PAIR in memory.max:402653184 memory.high:268435456 memory.swap.max:0 pids.max:64; do
  DASH_FILE="${DASH_PAIR%%:*}"
  DASH_LIMIT="${DASH_PAIR##*:}"
  DASH_VALUE="$(cat "$DASH_ROOT/$DASH_FILE")"
  [[ "$DASH_VALUE" =~ ^[0-9]+$ ]]
  (( DASH_VALUE <= DASH_LIMIT ))
  if (( DASH_LIMIT > 0 )); then (( DASH_VALUE > 0 )); fi
done
[[ "$(systemctl show dash.service --property=KillMode --value)" == control-group ]]
[[ "$(systemctl show dash.service --property=Restart --value)" == on-failure ]]
# The supervisor publishes this only after a matching backend has bound its
# listener and survived ten seconds. Bind it to this exact systemd process and
# a live child in the same cgroup, so stale markers cannot pass verification.
DASH_READY=/data/dash/cache/supervisor-ready.json
[[ -f "$DASH_READY" && "$(wc -c < "$DASH_READY")" -le 2048 ]]
DASH_MARKER="$(cat "$DASH_READY")"
DASH_BOOT_PID="$(sed -n 's/.*"bootstrap": *\([0-9][0-9]*\).*/\1/p' <<< "$DASH_MARKER")"
DASH_CHILD_PID="$(sed -n 's/.*"child": *\([0-9][0-9]*\).*/\1/p' <<< "$DASH_MARKER")"
DASH_READY_TAG="$(sed -n 's/.*"tag": *"\([a-zA-Z0-9.+_-]*\)".*/\1/p' <<< "$DASH_MARKER")"
[[ "$DASH_BOOT_PID" == "$(systemctl show dash.service --property=MainPID --value)" ]]
[[ "$DASH_CHILD_PID" =~ ^[1-9][0-9]*$ ]]
grep -qx "$DASH_CHILD_PID" "$DASH_ROOT/cgroup.procs"
[[ "$(readlink /data/dash/current)" == "releases/$DASH_READY_TAG" ]]
[[ ! -e /data/dash/activation.json ]]
echo 'Verified backend startup health and matching systemd supervisor/child.'
echo 'Verified kernel limits: CPU <=50% of one core, memory <=384 MiB, high <=256 MiB, swap=0, tasks <=64.'
systemctl show dash.service --property=ControlGroup,MemoryCurrent,CPUUsageNSec,TasksCurrent,KillMode,Restart,StartLimitBurst,StartLimitIntervalUSec
