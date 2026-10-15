#!/bin/bash
# /run is cleared at boot. Keep this guard ahead of comma.service so persisted
# driving flags cannot start Dash before the manager has published fresh state.
set -euo pipefail
umask 077
runtime=/run/dash
params=/data/params/d
while [[ $# -gt 0 ]]; do
  case "$1" in
    --runtime|--params)
      [[ $# -ge 2 && -n "$2" ]] || { echo 'Missing boot-state path' >&2; exit 2; }
      if [[ "$1" == --runtime ]]; then runtime="$2"; else params="$2"; fi
      shift 2
      ;;
    *) break ;;
  esac
done
[[ $# -ge 1 && -d "$runtime" && ! -L "$runtime" ]] || { echo 'Boot-state runtime directory is unavailable' >&2; exit 2; }
action="$1"
shift
prepared="$runtime/boot-prepared"
validated="$runtime/boot-validated"
marker_valid() { [[ -f "$validated" && ! -L "$validated" && "$(cat "$validated")" == validated ]]; }
fingerprint() { stat --format '%d:%i:%s:%y:%z' -- "$params/IsOffroad" 2>/dev/null; }
write_marker() {
  local destination="$1" value="$2" temporary
  temporary="$(mktemp "$runtime/.boot-state.XXXXXX")"
  if ! { printf '%s\n' "$value" > "$temporary" && mv -f -- "$temporary" "$destination"; }; then
    rm -f -- "$temporary"
    return 1
  fi
}
case "$action" in
  prepare)
    [[ $# == 0 ]] || { echo 'Unexpected prepare arguments' >&2; exit 2; }
    if marker_valid; then exit 0; fi
    # Preserve an early-boot baseline even if the manager becomes active before
    # a retry: its persisted flags still require a proven fresh publication.
    if [[ -f "$prepared" && ! -L "$prepared" && -s "$prepared" ]]; then exit 0; fi
    # A manual installation after the firmware manager is already active has no
    # prepared early-boot baseline. Go still enforces its normal state gate.
    if systemctl is-active --quiet comma.service; then
      write_marker "$validated" validated
      exit 0
    fi
    baseline="$(fingerprint)" || baseline=unavailable
    write_marker "$prepared" "$baseline"
    ;;
  run)
    [[ "${1:-}" != -- ]] || shift
    [[ $# -gt 0 ]] || { echo 'Missing supervised command' >&2; exit 2; }
    if ! marker_valid; then
      [[ -f "$prepared" && ! -L "$prepared" && -s "$prepared" ]] || { echo 'Boot-state prepare has not completed' >&2; exit 1; }
      baseline="$(cat "$prepared")"
      echo 'Waiting for fresh firmware driving state.'
      while ! marker_valid; do
        current="$(fingerprint)" || current=unavailable
        if [[ "$current" != unavailable && "$current" != "$baseline" ]]; then
          on="$(cat "$params/IsOnroad" 2>/dev/null)" || on=unavailable
          off="$(cat "$params/IsOffroad" 2>/dev/null)" || off=unavailable
          if [[ ( "$on" == 0 && "$off" == 1 ) || ( "$on" == 1 && "$off" == 0 ) ]]; then
            # Do not validate an IsOffroad file replaced while the pair was read.
            after="$(fingerprint)" || after=unavailable
            if [[ "$after" == "$current" ]]; then write_marker "$validated" validated; fi
          fi
        fi
        if ! marker_valid; then sleep 0.1; fi
      done
      echo 'Fresh firmware driving state received.'
    fi
    exec "$@"
    ;;
  *) echo 'Expected prepare or run' >&2; exit 2 ;;
esac
