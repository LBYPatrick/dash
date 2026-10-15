# Development tooling

Dash uses Vite+ 1.2.0 across the `frontend/` workspace and shared JavaScript scripts. Go remains a separate module in `backend/`.

## Setup

```sh
make setup-build-env
# For direct vp commands in this shell:
source .artifacts/toolchains/env.sh
```

The host setup downloads and verifies the native Vite+ CLI under `.artifacts/toolchains/vite-plus`, configures only that project-local environment, and provisions the Node version in `.node-version` plus the pnpm version in `package.json`. It reuses a compatible Go installation or installs a verified project-local SDK. It does not edit shell profiles or install anything on a comma device. macOS requires Command Line Tools; host package requirements are in [delivery.md](delivery.md#build-yourself-and-debug-installation).

An existing Vite+ 1.2.0 installation with the pinned Node runtime also works: run `vp install --frozen-lockfile` and `make install` from the root. `make install` additionally downloads Go modules. There is one `pnpm-lock.yaml` for the root tooling and frontend; do not create npm or per-package lockfiles.

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

The root `vite.config.ts` owns Oxfmt, Oxlint and shared test settings. Oxlint enables TypeScript and Vue plugins, type-aware linting and TypeScript diagnostics. Correctness findings are errors. Vendored Shelf code and generated outputs are excluded from lint; Dash-owned Vue components and scripts are checked. `vue-tsc` separately checks component templates; the generated `frontend/src/env.d.ts` lets the native TypeScript checker resolve component imports.

`frontend/vite.config.ts` owns the Vue app, backend proxy and PWA build. It keeps the repository-root ignored `.env` location. Secrets must never use a `VITE_` variable because those values are public in the built browser bundle.

The Vite+ package and Vite core alias must stay on the same exact version in both manifests and `pnpm-workspace.yaml`; keep its Vitest override aligned with the bundled runner. Follow [Vite+ upgrades](https://viteplus.dev/guide/upgrade-project) and run the complete checks before updating these pins.

## CI and delivery

Only `main`, `nightly` and `dev` commits start CI. The pinned `setup-vp` action reads the project runtime and toolchain pins, caches package-manager data and runs a frozen install. The Docker sandbox uses the official versioned Vite+ build image. The comma device still receives only Go, static browser assets and bundled media tools; Vite+ and Node are build-host dependencies.
