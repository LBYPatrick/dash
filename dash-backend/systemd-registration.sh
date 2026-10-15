#!/bin/bash
# Sourced by Dash's root-owned install/uninstall helpers. The only persistent
# system-partition changes are our unit and boot-enablement symlinks.
dash_registration_writable() { [[ -w /etc/systemd/system ]]; }

dash_systemd_registration() (
  set -euo pipefail
  action="$1"
  [[ "$action" == register || "$action" == remove ]]
  runtime="${2:-false}"
  options=()
  remounted=false
  restore_root() {
    code=$?
    trap - EXIT
    if "$remounted"; then
      if ! mount -o remount,ro /; then
        echo 'ERROR: Could not restore the root filesystem to read-only.' >&2
        exit 1
      fi
      echo 'Root filesystem restored to read-only.'
    fi
    exit "$code"
  }
  trap restore_root EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM
  trap 'exit 129' HUP
  if [[ "$runtime" == true ]]; then
    options=(--runtime)
  elif ! dash_registration_writable; then
    # Restrict temporary remount to the ordinary AGNOS root filesystem. Refuse
    # nested read-only mounts or an unexpected filesystem rather than guessing.
    target="$(findmnt -n -o TARGET --target /etc/systemd/system)"
    filesystem="$(findmnt -n -o FSTYPE --target /etc/systemd/system)"
    settings="$(findmnt -n -o OPTIONS --target /etc/systemd/system)"
    [[ "$target" == / && "$filesystem" == ext4 && ",$settings," == *,ro,* ]] || { echo 'Unsupported read-only unit registration mount'; exit 1; }
    echo 'Temporarily making AGNOS root writable for Dash systemd links only.'
    remounted=true
    mount -o remount,rw /
  fi
  if [[ "$action" == register ]]; then
    systemctl ${options[@]+"${options[@]}"} link /data/dash/systemd/dash.service
    systemctl ${options[@]+"${options[@]}"} enable dash.service
  else
    systemctl ${options[@]+"${options[@]}"} disable dash.service
    # systemctl normally removes both links. Remove an orphan only if it is
    # still exactly our own registration; never replace another application's.
    directory=/etc/systemd/system
    if [[ "$runtime" == true ]]; then directory=/run/systemd/system; fi
    if [[ -L "$directory/dash.service" && "$(readlink "$directory/dash.service")" == /data/dash/systemd/dash.service ]]; then rm "$directory/dash.service"; fi
  fi
)
