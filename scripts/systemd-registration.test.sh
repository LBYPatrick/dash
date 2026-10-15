#!/bin/bash
set -euo pipefail
project="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fixture="$(mktemp -d)"
trap 'rm -rf "$fixture"' EXIT
export DASH_REGISTRATION_LOG="$fixture/log"
cat > "$fixture/findmnt" <<'MOCK'
#!/bin/bash
case "$3" in TARGET) echo / ;; FSTYPE) echo ext4 ;; OPTIONS) echo ro,relatime ;; *) exit 2 ;; esac
MOCK
cat > "$fixture/mount" <<'MOCK'
#!/bin/bash
printf 'mount %s\n' "$*" >> "$DASH_REGISTRATION_LOG"
MOCK
cat > "$fixture/systemctl" <<'MOCK'
#!/bin/bash
printf 'systemctl %s\n' "$*" >> "$DASH_REGISTRATION_LOG"
if [[ "${DASH_TEST_FAIL_ENABLE:-}" == 1 && "$1" == enable ]]; then exit 17; fi
MOCK
chmod +x "$fixture/findmnt" "$fixture/mount" "$fixture/systemctl"
export PATH="$fixture:$PATH"
source "$project/dash-backend/systemd-registration.sh"
dash_registration_writable() { return 1; }
dash_systemd_registration register false
expected=$'mount -o remount,rw /\nsystemctl link /data/dash/systemd/dash.service\nsystemctl enable dash.service\nmount -o remount,ro /'
[[ "$(cat "$DASH_REGISTRATION_LOG")" == "$expected" ]]
: > "$DASH_REGISTRATION_LOG"
export DASH_TEST_FAIL_ENABLE=1
# Run as a separate shell so errexit stays enabled inside the function.
if bash -c 'source "$1"; dash_registration_writable() { return 1; }; dash_systemd_registration register false' bash "$project/dash-backend/systemd-registration.sh"; then echo 'Expected registration failure'; exit 1; fi
[[ "$(tail -n 1 "$DASH_REGISTRATION_LOG")" == 'mount -o remount,ro /' ]]
unset DASH_TEST_FAIL_ENABLE
: > "$DASH_REGISTRATION_LOG"
dash_systemd_registration register true
[[ "$(cat "$DASH_REGISTRATION_LOG")" == $'systemctl --runtime link /data/dash/systemd/dash.service\nsystemctl --runtime enable dash.service' ]]
echo 'Systemd registration passed: only Dash links, read-only restoration on success/failure, runtime mode never remounts.'
