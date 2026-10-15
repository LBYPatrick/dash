#!/bin/bash
# Exercise readiness verification without systemd, sudo or a real device.
set -euo pipefail
project="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fixture="$(mktemp -d)"
fixture="$(cd "$fixture" && pwd -P)"
trap 'rm -rf "$fixture"' EXIT
export DASH_VERIFY_ROOT="$fixture/root"
export DASH_VERIFY_ENABLED=true
export DASH_VERIFY_CHILD="$$"
mkdir -p "$fixture/bin" "$DASH_VERIFY_ROOT/data/dash/systemd" "$DASH_VERIFY_ROOT/data/dash/cache" "$DASH_VERIFY_ROOT/data/dash/releases/test" "$DASH_VERIFY_ROOT/run/dash" "$DASH_VERIFY_ROOT/etc/systemd/system/multi-user.target.wants"
for helper in verify-service.sh systemd-registration.sh; do
  "$DASH_TEST_HELPER" rewrite "$project/backend/$helper" "$fixture/$helper" "$DASH_VERIFY_ROOT"
done
cp "$fixture/systemd-registration.sh" "$DASH_VERIFY_ROOT/data/dash/"
cat > "$fixture/bin/systemctl" <<'MOCK'
#!/bin/bash
set -euo pipefail
case "$1" in
  is-active) exit 0 ;;
  is-enabled) "$DASH_VERIFY_ENABLED" ;;
  show)
    case "$3" in
      --property=FragmentPath) echo "$DASH_VERIFY_ROOT/etc/systemd/system/dash.service" ;;
      --property=KillMode) echo control-group ;;
      --property=Restart) echo on-failure ;;
      --property=MainPID) echo 123 ;;
      *) exit 2 ;;
    esac ;;
  *) exit 2 ;;
esac
MOCK
chmod +x "$fixture/bin/systemctl"
export PATH="$fixture/bin:$PATH"
app="$DASH_VERIFY_ROOT/data/dash"
unit="$DASH_VERIFY_ROOT/etc/systemd/system/dash.service"
wants="$(dirname "$unit")/multi-user.target.wants/dash.service"
printf '# Dash managed service. Source: %s/systemd/dash.service\n[Unit]\n' "$app" > "$app/systemd/dash.service"
cp "$app/systemd/dash.service" "$unit"
ln -s ../dash.service "$wants"
ln -s releases/test "$app/current"
printf '#!/bin/bash\nexit 0\n' > "$app/releases/test/dash-backend"
chmod +x "$app/releases/test/dash-backend"
touch "$app/boot-state.sh"
chmod +x "$app/boot-state.sh"
printf validated > "$DASH_VERIFY_ROOT/run/dash/boot-validated"
printf '{"bootstrap":123,"child":%s,"tag":"test"}\n' "$DASH_VERIFY_CHILD" > "$app/cache/supervisor-ready.json"
verify() { bash "$fixture/verify-service.sh" > "$fixture/result.log" 2>&1; }
expect_failure() {
  if verify; then cat "$fixture/result.log" >&2; echo 'Readiness verification incorrectly accepted broken registration' >&2; exit 1; fi
}
verify
export DASH_VERIFY_ENABLED=false
expect_failure
export DASH_VERIFY_ENABLED=true
rm "$wants"
expect_failure
ln -s "$app/systemd/dash.service" "$wants"
expect_failure
rm "$wants"
ln -s ../dash.service "$wants"
verify
rm "$DASH_VERIFY_ROOT/run/dash/boot-validated"
expect_failure
printf validated > "$DASH_VERIFY_ROOT/run/dash/boot-validated"
verify
# A legacy /data unit link may have worked after boot but cannot prove that
# systemd can load it before /data mounts. Verification rejects it.
rm "$unit"
ln -s "$app/systemd/dash.service" "$unit"
expect_failure
echo 'Service verification passed: physical unit and matching local enablement required; disabled, missing, legacy and misdirected registration rejected.'
