# Development tooling

Dash uses Go for host tooling and Vite+ 1.2.0 for frontend development, builds, and tests. There are no standalone JavaScript scripts or direct Node commands. The host CLI lives in `backend/cmd/dash-tool` with its implementation in `backend/internal/hosttools`; it is not linked into the device backend.

## Setup

```sh
make setup-build-env
# For direct vp commands in this shell:
source .artifacts/toolchains/env.sh
```

Install Go matching `backend/go.mod` first. Host setup uses Go to download and checksum-verify the pinned native Vite+ CLI when needed, then asks Vite+ to provision its managed environment. An existing pinned Vite+ installation is reused. Project-local state stays under ignored `.artifacts/toolchains/`; no shell profiles or comma devices are changed. macOS requires Command Line Tools; system/media prerequisites are in [delivery.md](delivery.md#build-yourself-and-debug-installation).

Developers invoke `go` and `vp`. Vite+ internally manages the JavaScript runtime pinned in `.node-version` and the package manager pinned in `package.json`; do not install or invoke Node/npm/pnpm separately. Dependency changes use `vp install` / `vp add`, with one root `pnpm-lock.yaml`.

## Commands

| Task                                                           | Command                        |
| -------------------------------------------------------------- | ------------------------------ |
| Install locked dependencies                                    | `vp install --frozen-lockfile` |
| Add a frontend dependency                                      | `vp -C frontend add PACKAGE`   |
| Add shared development tooling                                 | `vp add -Dw PACKAGE`           |
| Develop against the running backend                            | `make dev`                     |
| Format JavaScript, Vue, CSS and docs with Oxfmt                | `vp fmt`                       |
| Lint with Oxlint and fail on warnings                          | `make lint`                    |
| Check format, lint and TypeScript                              | `vp check`                     |
| Run all JavaScript tests                                       | `vp test run`                  |
| Build and check Vue templates                                  | `vp -C frontend run build`     |
| Audit accent contrast, modal focus and localized phone layouts | `make frontend-polish-smoke`   |
| Verify the whole project                                       | `make check`                   |

`vp dev` and `vp build` target `frontend/` by default through the root configuration. Built-in `vp build` runs the bundler; the frontend build task also runs `vue-tsc`. Use `make build` to include template checking and the Go binary. Release and source-install targets call that checked build path.

## Configuration

The root `vite.config.ts` owns Oxfmt, Oxlint and shared test settings. Oxlint enables TypeScript and Vue plugins, type-aware linting and TypeScript diagnostics. Correctness findings are errors. Vendored Shelf code and generated outputs are excluded from lint; Dash-owned Vue components and frontend tests are checked. `vue-tsc` separately checks component templates; the generated `frontend/src/env.d.ts` lets the native TypeScript checker resolve component imports.

`frontend/vite.config.ts` owns the Vue app, backend proxy and PWA build. It keeps the repository-root ignored `.env` location. Secrets must never use a `VITE_` variable because those values are public in the built browser bundle.

The Vite+ package and Vite core alias must stay on the same exact version in both manifests and `pnpm-workspace.yaml`; keep its Vitest override aligned with the bundled runner. Follow [Vite+ upgrades](https://viteplus.dev/guide/upgrade-project) and run the complete checks before updating these pins.

## CI and delivery

Only `main`, `nightly` and `dev` commits start CI. The pinned `setup-vp` action reads the project runtime and toolchain pins, caches package-manager data and runs a frozen install. The Docker sandbox uses the official versioned Vite+ build image. The comma device still receives only Go, static browser assets and bundled media tools. The publisher uses only Go and the GitHub CLI. The device does not receive the host tool or frontend development runtime.

## Host and browser workflows

Make targets dispatch packaging, publication, media preparation, SDK setup and sandbox management through `go -C backend run ./cmd/dash-tool`. Installer and bootstrap regression fixtures use the compiled Go test binary as their fake system tools. Standard `make check` needs no Python interpreter.

Browser acceptance lives in `frontend/tests/browser/` and runs through Vite+ with `frontend/browser.config.ts`. It is serial and opt-in; ordinary `vp test run` does not launch Chrome. For example:

```sh
DASH_TEST_URL=http://127.0.0.1:8088 make frontend-polish-smoke
vp test run --config frontend/browser.config.ts frontend/tests/browser/viewer-smoke.test.js
```

`DASH_MEDIA_OFFLINE=1` requires an already verified media cache and refuses downloads or Docker builds. This is useful when testing source packaging on a host without a running Docker engine.

`make build-filesystems MEDIA_ARCH=arm64|amd64` builds pinned static exFAT/NTFS helpers on the Docker build host. `make filesystem-smoke` tests fresh filesystem images in an isolated root mount namespace on native Linux; its host-only packages and requirements are listed in [exports.md](exports.md). `make transfers-smoke` covers the responsive export and authorization flow. The Docker sandbox includes a synthetic USB destination with persistent transfer state.

Go integration checks for Docker media, filesystem images, sandbox limits, OTA and source installation are enabled by their Make targets. Ordinary Go tests keep those workflows disabled. Upstream binary-log regeneration and independent MCAP verification remain optional maintenance tools documented with the [fixture provenance](../scripts/fixtures/README.md); their external schema tooling is not part of the normal development environment.

`make playback-smoke` covers desktop/mobile native preparation, disabled camera choices, browser capability feedback, notifications, and map controls. Native MP4 packaging tests decode synthetic output and compare every video frame against its source. The Go muxer uses the MIT-licensed `mp4ff` package; releases include its notice under `bin/licenses/mp4ff.txt`.

For a short, already-indexed physical session, verify native HTTPS reads without requesting preparation or changing firmware state:

```sh
DASH_DEVICE_ACCEPTANCE=1 DASH_TEST_URL=https://DEVICE_IP \
  DASH_DEVICE_CA="$PWD/tls/ca.crt" DASH_DEVICE_DRIVE=DRIVE_ID \
  DASH_DEVICE_OUTPUT="$PWD/.artifacts/native-device" \
  GOMAXPROCS=2 go -C backend test -p 1 -parallel 1 \
  -run '^TestPhysicalDeviceNativePlayback$' -v .
```

This check requires confirmed parked state, downloads bounded camera files to the ignored host output folder, and validates them with host FFmpeg. It never installs packages on the device or starts conversion jobs. `DASH_DEVICE_CA` also enables local HTTPS for the existing export acceptance test. To verify a previously downloaded real session archive entirely on the host, set `DASH_NATIVE_RECORDING_ZIP` and run `TestPhysicalRecordingNativePackaging`. Keep all private recordings under ignored `.artifacts/`; never commit them.
