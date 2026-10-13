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
echo 'SSH preflight check passed: no local builds/uploads or remote mutation commands; invalid targets refused.'
