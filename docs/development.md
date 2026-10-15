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

Go integration checks for Docker media, sandbox limits, OTA and source installation are enabled by their Make targets. Ordinary Go tests keep those workflows disabled. Upstream binary-log regeneration and independent MCAP verification remain optional maintenance tools documented with the [fixture provenance](../scripts/fixtures/README.md); their external schema tooling is not part of the normal development environment.
