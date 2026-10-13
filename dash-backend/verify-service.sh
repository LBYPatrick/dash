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
echo 'Verified kernel limits: CPU <=50% of one core, memory <=384 MiB, high <=256 MiB, swap=0, tasks <=64.'
systemctl show dash.service --property=ControlGroup,MemoryCurrent,CPUUsageNSec,TasksCurrent,KillMode,Restart,StartLimitBurst,StartLimitIntervalUSec
