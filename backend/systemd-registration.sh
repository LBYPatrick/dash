#!/bin/bash
# Sourced by Dash's root-owned install/uninstall helpers. The only persistent
# system-partition changes are our small unit copy and enablement symlink.
DASH_UNIT_MARKER='# Dash managed service. Source: /data/dash/systemd/dash.service'
DASH_USB_UNIT_MARKER='# Dash managed USB service. Source: /data/dash/systemd/dash-usb.service'
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

# Validate both layers before mutating either; /etc takes precedence over /run.
# A runtime install must never silently retain an existing boot registration.
dash_registration_present() {
  local directory="$1"
  [[ -e "$directory/dash.service" || -L "$directory/dash.service" ||
     -e "$directory/multi-user.target.wants/dash.service" || -L "$directory/multi-user.target.wants/dash.service" ||
     -e "$directory/dash-usb.service" || -L "$directory/dash-usb.service" ||
     -e "$directory/multi-user.target.wants/dash-usb.service" || -L "$directory/multi-user.target.wants/dash-usb.service" ]]
}

dash_validate_registration() {
  local directory="$1" wants target
  if [[ -e "$directory/dash.service" || -L "$directory/dash.service" ]]; then
    dash_unit_owned "$directory/dash.service" || { echo 'Refusing foreign Dash registration' >&2; return 1; }
  fi
  wants="$directory/multi-user.target.wants"
  if [[ -e "$wants/dash.service" || -L "$wants/dash.service" ]]; then
    [[ -L "$wants/dash.service" ]] || { echo 'Refusing foreign Dash enablement' >&2; return 1; }
    target="$(readlink "$wants/dash.service")"
    [[ "$target" == /data/dash/systemd/dash.service || "$target" == ../dash.service || "$target" == "$directory/dash.service" ]] || { echo 'Refusing foreign Dash enablement' >&2; return 1; }
  fi
}

dash_validate_registrations() {
  dash_validate_registration /etc/systemd/system || return 1
  dash_validate_registration /run/systemd/system || return 1
  dash_validate_usb_registration /etc/systemd/system || return 1
  dash_validate_usb_registration /run/systemd/system
}

dash_validate_usb_registration() {
  local directory="$1" file="$1/dash-usb.service" link="$1/multi-user.target.wants/dash-usb.service" target
  if [[ -e "$file" || -L "$file" ]]; then
    [[ -f "$file" && ! -L "$file" && "$(head -n 1 "$file")" == "$DASH_USB_UNIT_MARKER" ]] || { echo 'Refusing foreign USB helper registration' >&2; return 1; }
  fi
  if [[ -e "$link" || -L "$link" ]]; then
    [[ -L "$link" ]] || { echo 'Refusing foreign USB helper enablement' >&2; return 1; }
    target="$(readlink "$link")"
    [[ "$target" == ../dash-usb.service || "$target" == "$directory/dash-usb.service" ]] || { echo 'Refusing foreign USB helper enablement' >&2; return 1; }
  fi
}

dash_runtime_install_allowed() {
  if dash_registration_present /etc/systemd/system; then
    echo 'A persistent Dash registration already exists. Keep persistent mode, or uninstall Dash before a runtime-only installation.' >&2
    return 1
  fi
}

dash_remove_registrations() {
  dash_validate_registrations || return 1
  dash_systemd_registration remove true || return 1
  dash_systemd_registration remove false
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
  dash_validate_registrations
  if [[ "$action" == register && "$runtime" == true ]]; then dash_runtime_install_allowed; fi
  if [[ "$action" == remove ]] && ! dash_registration_present "$directory"; then exit 0; fi
  wants="$directory/multi-user.target.wants"
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
    if [[ -f /data/dash/systemd/dash-usb.service ]]; then
      install -o root -g root -m 644 /data/dash/systemd/dash-usb.service "$directory/dash-usb.service.next"
      sync -- "$directory/dash-usb.service.next"
      mv -Tf "$directory/dash-usb.service.next" "$directory/dash-usb.service"
      ln -sfn ../dash-usb.service "$wants/dash-usb.service.next"
      mv -Tf "$wants/dash-usb.service.next" "$wants/dash-usb.service"
      sync -- "$wants" "$directory"
      systemctl ${options[@]+"${options[@]}"} enable dash-usb.service
    fi
    systemctl ${options[@]+"${options[@]}"} enable dash.service
    # A durable install supersedes a previous temporary installation.
    if [[ "$runtime" != true ]]; then dash_systemd_registration remove true; fi
  else
    # systemctl disable may fail when the unit is gone but a dangling wants
    # link survived a crash. Its ownership was validated before any mutation.
    if [[ -e "$directory/dash.service" || -L "$directory/dash.service" ]]; then
      systemctl ${options[@]+"${options[@]}"} disable dash.service
      dash_unit_owned "$directory/dash.service" && rm "$directory/dash.service"
    fi
    if [[ -L "$wants/dash.service" ]]; then rm "$wants/dash.service"; fi
    if [[ -f "$directory/dash-usb.service" ]]; then
      systemctl ${options[@]+"${options[@]}"} disable dash-usb.service
      rm "$directory/dash-usb.service"
    fi
    if [[ -L "$wants/dash-usb.service" ]]; then rm "$wants/dash-usb.service"; fi
    if [[ -d "$wants" ]]; then sync -- "$wants"; fi
    sync -- "$directory"
  fi
)
