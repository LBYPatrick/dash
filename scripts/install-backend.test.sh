#!/bin/bash
set -euo pipefail
project="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
mock_dir=$(mktemp -d)
trap 'rm -rf "$mock_dir"' EXIT
cat > "$mock_dir/ssh" <<'MOCK'
#!/bin/bash
set -euo pipefail
cat > "$DASH_PREFLIGHT_CAPTURE"
MOCK
chmod +x "$mock_dir/ssh"
for tool in npm scp go; do
  cat > "$mock_dir/$tool" <<'MOCK'
#!/bin/bash
exit 99
MOCK
  chmod +x "$mock_dir/$tool"
done
PATH="$mock_dir:$PATH" DASH_PREFLIGHT_CAPTURE="$mock_dir/remote.sh" bash "$project/scripts/install-backend.sh" --check comma@fixture
bash -n "$mock_dir/remote.sh"
if rg 'systemctl (start|stop|enable)|mkdir|rm -|mv |tee |WriteFile' "$mock_dir/remote.sh"; then exit 1; fi
if bash "$project/scripts/install-backend.sh" --check 'comma@bad;host' >/dev/null 2>&1; then exit 1; fi
PATH="$mock_dir:$PATH" DASH_PREFLIGHT_CAPTURE="$mock_dir/remote.sh" bash "$project/scripts/install-backend.sh" --check --channel nightly --release v0.1.0-nightly.1.aaaaaaaa comma@fixture
if bash "$project/scripts/install-backend.sh" --check --channel feature comma@fixture >/dev/null 2>&1; then exit 1; fi
if bash "$project/scripts/install-backend.sh" --check --repo 'owner/repo;cmd' comma@fixture >/dev/null 2>&1; then exit 1; fi
PATH="$mock_dir:$PATH" DASH_PREFLIGHT_CAPTURE="$mock_dir/remote.sh" bash "$project/scripts/install-backend.sh" --check --source comma@fixture
if bash "$project/scripts/install-backend.sh" --source --channel dev comma@fixture >/dev/null 2>&1; then exit 1; fi
echo 'SSH preflight check passed: no local builds/uploads or remote mutation commands; invalid targets refused.'
# Inject failures at each atomic boot-file replacement boundary. A failed copy,
# file flush or rename must leave the original complete executable available.
python3 - "$project" "$mock_dir" <<'PY'
import os
from pathlib import Path
import subprocess
import sys

project, fixture = map(Path, sys.argv[1:])
script = (project / 'scripts/install-backend.sh').read_text()
start = script.index('dash_install_atomic() {')
function = script[start:script.index('\n}', start) + 2]
mock = fixture / 'atomic-command.py'
mock.write_text('''import os, shutil, sys
from pathlib import Path
command, *args = sys.argv[1:]
phase = command
if command == 'sync': phase = 'sync-dir' if Path(args[-1]).is_dir() else 'sync-file'
with open(os.environ['DASH_ATOMIC_LOG'], 'a') as output: output.write(phase + '\\n')
if os.environ.get('DASH_ATOMIC_FAIL') == phase:
    if command == 'install': Path(args[-1]).write_text('incomplete')
    sys.exit(17)
if command == 'install':
    shutil.copyfile(args[-2], args[-1])
    os.chmod(args[-1], int(args[args.index('-m') + 1], 8))
elif command == 'mv': os.replace(args[-2], args[-1])
elif command == 'sync':
    # The host may not support fsync on directories. The remote GNU sync does;
    # exercise the ordering here and perform a real fsync for file contents.
    if not Path(args[-1]).is_dir():
        with open(args[-1], 'rb') as source: os.fsync(source.fileno())
else: raise AssertionError(command)
''')
source, destination = fixture / 'payload', fixture / 'bootstrap'
source.write_bytes(b'complete new executable')
for failure in ('install', 'sync-file', 'mv', 'sync-dir', ''):
    destination.write_bytes(b'complete old executable')
    log = fixture / 'atomic.log'
    log.write_text('')
    env = {**os.environ, 'DASH_ATOMIC_LOG': str(log), 'DASH_ATOMIC_FAIL': failure}
    result = subprocess.run(['bash', '-c', '''set -euo pipefail
sudo() { [[ "$1" == -n ]]; shift; python3 "$DASH_ATOMIC_MOCK" "$@"; }
''' + function + '\ndash_install_atomic "$1" "$2" 755', 'bash', str(source), str(destination)],
        env={**env, 'DASH_ATOMIC_MOCK': str(mock)}, check=False)
    assert (result.returncode == 0) == (failure == ''), (failure, result.returncode)
    expected = source.read_bytes() if failure in ('sync-dir', '') else b'complete old executable'
    assert destination.read_bytes() == expected, failure
    if not failure:
        assert log.read_text().splitlines() == ['install', 'sync-file', 'mv', 'sync-dir']
        assert destination.stat().st_mode & 0o777 == 0o755
print('Atomic boot-file replacement passed: partial copy, flush and rename failures preserve a complete executable.')
PY
