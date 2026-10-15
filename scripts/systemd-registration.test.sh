#!/bin/bash
set -euo pipefail
project="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fixture="$(mktemp -d)"
fixture="$(cd "$fixture" && pwd -P)"
trap 'rm -rf "$fixture"' EXIT
export DASH_TEST_ROOT="$fixture/root"
export DASH_REGISTRATION_LOG="$fixture/commands.log"
mkdir -p "$fixture/bin"
# Run the real helper against an isolated filesystem. Absolute device paths are
# rewritten in its text, including ownership checks, rather than touching /etc.
python3 - "$project/backend/systemd-registration.sh" "$fixture/registration.sh" "$DASH_TEST_ROOT" <<'PY'
from pathlib import Path
import sys
source, target, root = sys.argv[1:]
text = Path(source).read_text()
for prefix in ('/etc/systemd/system', '/run/systemd/system', '/data/dash'):
    text = text.replace(prefix, root + prefix)
Path(target).write_text(text)
PY
cat > "$fixture/bin/mock-tool" <<'PY'
#!/usr/bin/env python3
from pathlib import Path
import os
import shutil
import sys

name = Path(sys.argv[0]).name
args = sys.argv[1:]
root = Path(os.environ['DASH_TEST_ROOT'])
with open(os.environ['DASH_REGISTRATION_LOG'], 'a') as log:
    log.write(name + ' ' + ' '.join(args) + '\n')
if name == 'findmnt':
    key = args[args.index('-o') + 1]
    print({'TARGET': '/', 'FSTYPE': 'ext4', 'OPTIONS': 'ro,relatime'}[key])
elif name == 'mount':
    if args != ['-o', 'remount,rw', '/'] and args != ['-o', 'remount,ro', '/']:
        raise SystemExit('Unexpected mount invocation')
elif name == 'install':
    directory = False
    mode = 0o755
    paths = []
    cursor = iter(args)
    for arg in cursor:
        if arg in ('-o', '-g'):
            next(cursor)
        elif arg == '-m':
            mode = int(next(cursor), 8)
        elif arg == '-d':
            directory = True
        else:
            paths.append(Path(arg))
    if directory:
        for path in paths:
            path.mkdir(parents=True, exist_ok=True)
            path.chmod(mode)
    else:
        shutil.copyfile(*paths)
        paths[-1].chmod(mode)
elif name == 'sync':
    if os.environ.get('DASH_TEST_FAIL_SYNC') == '1' and args[-1].endswith('.next'):
        raise SystemExit(19)
    for arg in args:
        if arg == '--':
            continue
        descriptor = os.open(arg, os.O_RDONLY)
        try:
            os.fsync(descriptor)
        finally:
            os.close(descriptor)
elif name == 'mv':
    if args[0] != '-Tf':
        raise SystemExit('Unexpected mv invocation')
    os.replace(args[1], args[2])
elif name == 'readlink':
    if args[:2] == ['-f', '--']:
        print(os.path.realpath(args[-1]))
    else:
        print(os.readlink(args[-1]))
elif name == 'systemctl':
    runtime = '--runtime' in args
    action = next(arg for arg in args if not arg.startswith('-'))
    if action == 'enable' and os.environ.get('DASH_TEST_FAIL_ENABLE') == '1':
        raise SystemExit(17)
    if action == 'disable':
        directory = root / ('run' if runtime else 'etc') / 'systemd/system'
        if not (directory / 'dash.service').exists():
            raise SystemExit('Unit missing: disable cannot repair orphan enablement')
        (directory / 'multi-user.target.wants/dash.service').unlink(missing_ok=True)
else:
    raise SystemExit('Unexpected mock tool ' + name)
PY
chmod +x "$fixture/bin/mock-tool"
for command in findmnt mount install sync mv readlink systemctl; do
  ln -s mock-tool "$fixture/bin/$command"
done
export PATH="$fixture/bin:$PATH"
unit="$DASH_TEST_ROOT/etc/systemd/system/dash.service"
wants="$DASH_TEST_ROOT/etc/systemd/system/multi-user.target.wants/dash.service"
runtime_unit="$DASH_TEST_ROOT/run/systemd/system/dash.service"
source_unit="$DASH_TEST_ROOT/data/dash/systemd/dash.service"
marker="# Dash managed service. Source: $source_unit"
reset_fixture() {
  rm -rf "$DASH_TEST_ROOT"
  mkdir -p "$(dirname "$source_unit")" "$(dirname "$wants")" "$(dirname "$runtime_unit")/multi-user.target.wants"
  printf '%s\n[Unit]\nRequiresMountsFor=/data\n[Service]\nExecStart=/data/dash/dash-bootstrap\n' "$marker" > "$source_unit"
  : > "$DASH_REGISTRATION_LOG"
  unset DASH_TEST_FAIL_ENABLE DASH_TEST_FAIL_SYNC
}
register() {
  bash -c 'set -euo pipefail; source "$1"; dash_registration_writable() { return 1; }; if [[ "$2" == remove-all ]]; then dash_remove_registrations; else dash_systemd_registration "$2" "$3"; fi' bash "$fixture/registration.sh" "$1" "${2:-false}" > "$fixture/result.log" 2>&1 || { code=$?; cat "$fixture/result.log" >&2; return "$code"; }
}
fail() { echo "FAIL: $*" >&2; cat "$fixture/result.log" >&2; exit 1; }
expect_failure() {
  if register "$@" 2>/dev/null; then fail "Expected $* to be refused"; fi
}
assert_remounted_read_only() {
  [[ "$(rg '^mount ' "$DASH_REGISTRATION_LOG" | tail -n 1)" == 'mount -o remount,ro /' ]] || fail 'Root was not restored read-only'
  [[ "$(rg -c '^mount -o remount,rw /$' "$DASH_REGISTRATION_LOG")" == 1 ]] || fail 'Expected one writable remount'
}
assert_not_called() {
  if rg -q "$1" "$DASH_REGISTRATION_LOG"; then fail "Unexpected command matching $1"; fi
}

# Migrate both historical links, then model the early boot phase before /data
# mounts: the enabled unit must still be readable and describe its mount need.
reset_fixture
ln -s "$source_unit" "$unit"
ln -s "$source_unit" "$wants"
register register
[[ -f "$unit" && ! -L "$unit" ]] || fail 'Boot unit must be a physical file'
[[ "$(readlink "$wants")" == ../dash.service ]] || fail 'Enablement must refer to the local boot unit'
cmp "$source_unit" "$unit" || fail 'Boot copy differs from installed source'
assert_remounted_read_only
mv -Tf "$DASH_TEST_ROOT/data" "$DASH_TEST_ROOT/data-unmounted"
[[ -r "$unit" && -r "$wants" ]] || fail 'Boot registration depends on mounted /data'
rg -q '^RequiresMountsFor=/data$' "$wants" || fail 'Boot unit lost mount dependency'

# A managed physical copy can be refreshed, and publication is ordered after
# fsync. Failure before the atomic rename must preserve the prior boot unit.
reset_fixture
printf '%s\nold unit\n' "$marker" > "$unit"
cp "$unit" "$fixture/prior-unit"
export DASH_TEST_FAIL_SYNC=1
expect_failure register
cmp "$unit" "$fixture/prior-unit" || fail 'Failed publication damaged existing unit'
assert_not_called '^mv '
assert_remounted_read_only
unset DASH_TEST_FAIL_SYNC
register register
cmp "$unit" "$source_unit" || fail 'Managed unit refresh failed'
python3 - "$DASH_REGISTRATION_LOG" "$unit" <<'PY'
import sys
from pathlib import Path
lines = Path(sys.argv[1]).read_text().splitlines()
unit = sys.argv[2]
assert lines.index('sync -- ' + unit + '.next') < lines.index('mv -Tf ' + unit + '.next ' + unit)
PY

reset_fixture
export DASH_TEST_FAIL_ENABLE=1
expect_failure register
assert_remounted_read_only

# Foreign fragments and foreign enablement are never replaced or disabled.
for action in register remove; do
  reset_fixture
  printf 'Foreign service\n' > "$unit"
  cp "$unit" "$fixture/foreign-unit"
  expect_failure "$action"
  cmp "$unit" "$fixture/foreign-unit" || fail 'Foreign unit was changed'
  assert_not_called '^systemctl '
  assert_not_called '^install '
  assert_not_called '^mount '

  reset_fixture
  ln -s "$DASH_TEST_ROOT/other.service" "$unit"
  expect_failure "$action"
  [[ "$(readlink "$unit")" == "$DASH_TEST_ROOT/other.service" ]] || fail 'Foreign fragment symlink was changed'
  assert_not_called '^systemctl '

  reset_fixture
  cp "$source_unit" "$unit"
  printf '\nold managed version\n' >> "$unit"
  cp "$unit" "$fixture/prior-unit"
  ln -s "$DASH_TEST_ROOT/other.service" "$wants"
  expect_failure "$action"
  [[ "$(readlink "$wants")" == "$DASH_TEST_ROOT/other.service" ]] || fail 'Foreign enablement was changed'
  cmp "$unit" "$fixture/prior-unit" || fail 'Foreign enablement refusal changed existing unit'
  assert_not_called '^systemctl '
  assert_not_called '^install '
  assert_not_called '^mount '

  reset_fixture
  cp "$source_unit" "$unit"
  printf 'foreign enablement file\n' > "$wants"
  cp "$wants" "$fixture/foreign-wants"
  expect_failure "$action"
  cmp "$wants" "$fixture/foreign-wants" || fail 'Foreign enablement file was changed'
  assert_not_called '^systemctl '
  assert_not_called '^install '
  assert_not_called '^mount '
done

# Uninstall removes the managed unit and its enablement, leaving neighbors and
# the app's own source untouched. Both physical copies and legacy links work.
for kind in copy legacy; do
  reset_fixture
  if [[ "$kind" == copy ]]; then cp "$source_unit" "$unit"; else ln -s "$source_unit" "$unit"; fi
  ln -s ../dash.service "$wants"
  printf 'keep\n' > "$(dirname "$unit")/unrelated.service"
  register remove
  [[ ! -e "$unit" && ! -L "$unit" && ! -e "$wants" && ! -L "$wants" ]] || fail 'Managed registration survived uninstall'
  [[ -f "$source_unit" && "$(cat "$(dirname "$unit")/unrelated.service")" == keep ]] || fail 'Uninstall touched unrelated files'
  assert_remounted_read_only
done

reset_fixture
register register true
[[ -f "$runtime_unit" && ! -L "$runtime_unit" ]] || fail 'Runtime unit must also be a physical file'
[[ -r "$(dirname "$runtime_unit")/multi-user.target.wants/dash.service" ]] || fail 'Runtime enablement is unreadable'
assert_not_called '^mount '
assert_not_called '^findmnt '
rg -q '^systemctl --runtime enable dash.service$' "$DASH_REGISTRATION_LOG" || fail 'Missing runtime enablement'
register remove true
[[ ! -e "$runtime_unit" ]] || fail 'Runtime uninstall left its unit'
assert_not_called '^mount '

# Runtime requests must fail without touching an existing persistent unit,
# including an orphan enablement left after interrupted installation.
for kind in unit wants; do
  reset_fixture
  if [[ "$kind" == unit ]]; then cp "$source_unit" "$unit"; else ln -s ../dash.service "$wants"; fi
  expect_failure register true
  assert_not_called '^mount '
  assert_not_called '^systemctl '
  assert_not_called '^install '
done

# Promoting a temporary install publishes the durable copy before removing
# /run, so an interruption cannot erase the only available registration.
reset_fixture
register register true
: > "$DASH_REGISTRATION_LOG"
register register
[[ -f "$unit" && -r "$wants" && ! -e "$runtime_unit" ]] || fail 'Persistent promotion retained runtime registration'
python3 - "$DASH_REGISTRATION_LOG" <<'PYORDER'
from pathlib import Path
import sys
lines = Path(sys.argv[1]).read_text().splitlines()
assert lines.index('systemctl enable dash.service') < lines.index('systemctl --runtime disable dash.service')
PYORDER
assert_remounted_read_only

# Remove both layers irrespective of startup-mode or loaded FragmentPath.
# Even a dangling wants link is cleaned, while an empty install needs no remount.
reset_fixture
for directory in "$(dirname "$unit")" "$(dirname "$runtime_unit")"; do
  cp "$source_unit" "$directory/dash.service"
  ln -s ../dash.service "$directory/multi-user.target.wants/dash.service"
done
register remove-all
[[ ! -e "$unit" && ! -e "$wants" && ! -e "$runtime_unit" ]] || fail 'Removal missed a registration layer'
assert_remounted_read_only
reset_fixture
ln -s ../dash.service "$wants"
register remove-all
[[ ! -L "$wants" ]] || fail 'Removal missed dangling enablement'
reset_fixture
register remove-all
assert_not_called '^mount '
assert_not_called '^systemctl '

# A hidden foreign layer must be detected before any other layer is touched.
reset_fixture
cp "$source_unit" "$unit"
printf 'Foreign service\n' > "$runtime_unit"
cp "$unit" "$fixture/prior-unit"
for action in register remove-all; do
  expect_failure "$action"
  cmp "$unit" "$fixture/prior-unit" || fail 'Refusal changed persistent unit'
  assert_not_called '^mount '
  assert_not_called '^systemctl '
done

echo 'Systemd registration passed: boot-readable physical unit, atomic migration, foreign registration protection, scoped removal, read-only restoration, and remount-free runtime mode.'
