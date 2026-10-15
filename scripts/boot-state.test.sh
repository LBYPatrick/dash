#!/bin/bash
set -euo pipefail
project="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fixture="$(mktemp -d)"
child_pid=
cleanup() {
  if [[ -n "$child_pid" ]]; then kill "$child_pid" 2>/dev/null || true; wait "$child_pid" 2>/dev/null || true; fi
  rm -rf "$fixture"
}
trap cleanup EXIT
mkdir -p "$fixture/bin"
cat > "$fixture/bin/stat" <<'PY'
#!/usr/bin/env python3
import os
import sys
try:
    value = os.lstat(sys.argv[-1])
except OSError:
    sys.exit(1)
print(f'{value.st_dev}:{value.st_ino}:{value.st_size}:{value.st_mtime_ns}:{value.st_ctime_ns}')
PY
cat > "$fixture/bin/systemctl" <<'MOCK'
#!/bin/bash
[[ "$*" == 'is-active --quiet comma.service' ]] || exit 2
[[ "${DASH_TEST_COMMA_ACTIVE:-0}" == 1 ]]
MOCK
cat > "$fixture/child" <<'MOCK'
#!/bin/bash
printf '%s\0' "$@" > "$DASH_TEST_CHILD_RESULT"
MOCK
chmod +x "$fixture/bin/stat" "$fixture/bin/systemctl" "$fixture/child"
export PATH="$fixture/bin:$PATH"
export DASH_TEST_CHILD_RESULT="$fixture/child-result"
run_guard() { bash "$project/backend/boot-state.sh" --runtime "$fixture/run" --params "$fixture/params" "$@"; }
reset_fixture() {
  if [[ -n "$child_pid" ]]; then kill "$child_pid" 2>/dev/null || true; wait "$child_pid" 2>/dev/null || true; child_pid=; fi
  rm -rf "$fixture/run" "$fixture/params" "$DASH_TEST_CHILD_RESULT"
  mkdir -p "$fixture/run" "$fixture/params"
  unset DASH_TEST_COMMA_ACTIVE
}
flags() {
  # Model the atomic replacement used by the firmware's parameter store.
  printf '%s' "$1" > "$fixture/params/IsOnroad.next"
  printf '%s' "$2" > "$fixture/params/IsOffroad.next"
  mv -f "$fixture/params/IsOnroad.next" "$fixture/params/IsOnroad"
  mv -f "$fixture/params/IsOffroad.next" "$fixture/params/IsOffroad"
}
start_child() {
  bash "$project/backend/boot-state.sh" --runtime "$fixture/run" --params "$fixture/params" run "$fixture/child" 'one two' '' 'literal $HOME' $'line\nbreak' > "$fixture/guard.log" 2>&1 &
  child_pid=$!
}
fail() { echo "FAIL: $*" >&2; cat "$fixture/guard.log" >&2 2>/dev/null || true; exit 1; }
assert_waiting() {
  sleep 0.25
  kill -0 "$child_pid" 2>/dev/null || fail 'Guard exited before receiving fresh state'
  [[ ! -e "$DASH_TEST_CHILD_RESULT" && ! -e "$fixture/run/boot-validated" ]] || fail 'Child started with stale or incoherent state'
}
assert_started() {
  for ((attempt=0; attempt<50; attempt++)); do
    if [[ -f "$DASH_TEST_CHILD_RESULT" ]]; then break; fi
    sleep 0.05
  done
  [[ -f "$DASH_TEST_CHILD_RESULT" ]] || fail 'Fresh state did not release child'
  wait "$child_pid" || fail 'Guard/child failed'
  child_pid=
  printf '%s\0' 'one two' '' 'literal $HOME' $'line\nbreak' > "$fixture/expected-args"
  cmp "$fixture/expected-args" "$DASH_TEST_CHILD_RESULT" || fail 'Command arguments changed'
  [[ "$(cat "$fixture/run/boot-validated")" == validated ]] || fail 'Validated marker missing'
}

# Old coherent parked flags must never release the child, even after repeated
# prepare calls and time passing. Fresh replacement is the release condition.
reset_fixture
flags 0 1
run_guard prepare
cp "$fixture/run/boot-prepared" "$fixture/baseline"
# A retry after comma.service becomes active must retain this baseline rather
# than treating the retry as a late installation and accepting persisted flags.
export DASH_TEST_COMMA_ACTIVE=1
run_guard prepare
cmp "$fixture/baseline" "$fixture/run/boot-prepared"
[[ ! -e "$fixture/run/boot-validated" ]] || fail 'Active manager bypassed an existing stale baseline'
unset DASH_TEST_COMMA_ACTIVE
start_child
assert_waiting
flags 0 1
assert_started

# Missing/malformed states remain locked out. A later fresh coherent parked
# pair starts the command, without modifying any firmware parameters itself.
reset_fixture
run_guard prepare
start_child
assert_waiting
printf malformed > "$fixture/params/IsOnroad"
printf 1 > "$fixture/params/IsOffroad"
assert_waiting
flags 1 1
assert_waiting
flags 0 0
assert_waiting
flags 0 1
assert_started
[[ "$(cat "$fixture/params/IsOnroad")$(cat "$fixture/params/IsOffroad")" == 01 ]] || fail 'Guard changed firmware state'

# Fresh driving state is allowed to start the Go supervisor: its existing state
# gate, rather than this freshness handshake, keeps heavy workers stopped.
reset_fixture
flags 0 1
run_guard prepare
start_child
assert_waiting
flags 1 0
assert_started

# Newer firmware publishes only IsOffroad. Its old value still cannot release
# boot, but a fresh 1 or 0 starts the server without an IsOnroad dependency.
for value in 1 0; do
  reset_fixture
  printf 1 > "$fixture/params/IsOffroad"
  run_guard prepare
  start_child
  assert_waiting
  printf '%s' "$value" > "$fixture/params/IsOffroad.next"
  mv -f "$fixture/params/IsOffroad.next" "$fixture/params/IsOffroad"
  assert_started
done

# Once validated, restart preparation and run remain idempotent this boot,
# including when the normal Go state reader will subsequently see unknown.
rm -f "$fixture/params/IsOnroad" "$fixture/params/IsOffroad" "$DASH_TEST_CHILD_RESULT"
run_guard prepare
start_child
assert_started

# Late installation after comma.service is active skips the boot-only wait.
reset_fixture
flags 0 1
export DASH_TEST_COMMA_ACTIVE=1
run_guard prepare
[[ ! -e "$fixture/run/boot-prepared" ]] || fail 'Late install unnecessarily captured a stale baseline'
unset DASH_TEST_COMMA_ACTIVE
run_guard prepare
start_child
assert_started

echo 'Boot-state guard passed: stale flags blocked; absent/malformed state waits; fresh parked/driving state releases; late installation and same-boot restart work; command arguments preserved.'
