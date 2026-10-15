#!/bin/bash
# Sourced by Dash's root-owned install/uninstall helpers. The only persistent
# system-partition changes are our small unit copy and enablement symlink.
DASH_UNIT_MARKER='# Dash managed service. Source: /data/dash/systemd/dash.service'
dash_registration_writable() { [[ -w /etc/systemd/system ]]; }

# Accept the legacy /data link for migration, or our root-owned unit copy.
dash_unit_owned() {
  local fragment="$1"
  [[ -e "$fragment" || -L "$fragment" ]] || return 1
  if [[ -L "$fragment" ]]; then
    [[ "$(readlink -f -- "$fragment")" == /data/dash/systemd/dash.service ]]
  else
    [[ "$fragment" == /etc/systemd/system/dash.service || "$fragment" == /run/systemd/system/dash.service ]] &&
      [[ -f "$fragment" && "$(head -n 1 "$fragment")" == "$DASH_UNIT_MARKER" ]]
  fi
}

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
  directory=/etc/systemd/system
  if [[ "$runtime" == true ]]; then directory=/run/systemd/system; fi
  if [[ -e "$directory/dash.service" || -L "$directory/dash.service" ]]; then
    dash_unit_owned "$directory/dash.service" || { echo 'Refusing foreign Dash registration'; exit 1; }
  fi
  wants="$directory/multi-user.target.wants"
  if [[ -e "$wants/dash.service" || -L "$wants/dash.service" ]]; then
    [[ -L "$wants/dash.service" ]] || { echo 'Refusing foreign Dash enablement'; exit 1; }
    target="$(readlink "$wants/dash.service")"
    [[ "$target" == /data/dash/systemd/dash.service || "$target" == ../dash.service || "$target" == "$directory/dash.service" ]] || { echo 'Refusing foreign Dash enablement'; exit 1; }
  fi
  if [[ "$runtime" == true ]]; then
    options=(--runtime)
  elif ! dash_registration_writable; then
    # Restrict temporary remount to the ordinary AGNOS root filesystem. Refuse
    # nested read-only mounts or an unexpected filesystem rather than guessing.
    target="$(findmnt -n -o TARGET --target /etc/systemd/system)"
    filesystem="$(findmnt -n -o FSTYPE --target /etc/systemd/system)"
    settings="$(findmnt -n -o OPTIONS --target /etc/systemd/system)"
    [[ "$target" == / && "$filesystem" == ext4 && ",$settings," == *,ro,* ]] || { echo 'Unsupported read-only unit registration mount'; exit 1; }
    echo 'Temporarily making AGNOS root writable for Dash systemd registration only.'
    remounted=true
    mount -o remount,rw /
  fi
  if [[ "$action" == register ]]; then
    # /data is unavailable when systemd initially loads the boot transaction.
    # A physical unit copy exposes RequiresMountsFor before its payload mounts.
    install -o root -g root -m 644 /data/dash/systemd/dash.service "$directory/dash.service.next"
    sync -- "$directory/dash.service.next"
    mv -Tf "$directory/dash.service.next" "$directory/dash.service"
    sync -- "$directory"
    install -d -o root -g root -m 755 "$wants"
    ln -sfn ../dash.service "$wants/dash.service.next"
    mv -Tf "$wants/dash.service.next" "$wants/dash.service"
    sync -- "$wants" "$directory"
    systemctl ${options[@]+"${options[@]}"} enable dash.service
  else
    systemctl ${options[@]+"${options[@]}"} disable dash.service
    # Remove only our copied unit (or its legacy symlink) after disablement.
    if [[ -e "$directory/dash.service" || -L "$directory/dash.service" ]]; then
      dash_unit_owned "$directory/dash.service" && rm "$directory/dash.service"
    fi
    sync -- "$directory"
  fi
)
