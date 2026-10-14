#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../dash-backend"
# Checked-in generated Go is used by ordinary builds, including on-device builds.
# Install the generator matching go.mod; protoc is a development-only tool.
mkdir -p ../.artifacts/bin
GOBIN="$PWD/../.artifacts/bin" go install google.golang.org/protobuf/cmd/protoc-gen-go@"$(go list -m -f '{{.Version}}' google.golang.org/protobuf)"
export PATH="$PWD/../.artifacts/bin:$PATH"
options=()
for name in CompressedVideo LocationFix Color KeyValuePair Vector3; do
  options+=("--go_opt=Mfoxglove/$name.proto=dash.local/backend/internal/mcaptypes/foxglove")
done
protoc -I schemas --go_out=. --go_opt=module=dash.local/backend "${options[@]}" schemas/dash/v2/telemetry.proto schemas/foxglove/*.proto
