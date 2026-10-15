<p align="center">
  <img src="frontend/public/icon.svg" alt="Dash" width="80" />
</p>

<h1 align="center">Dash</h1>

<p align="center">
  <strong>Your drive recordings, directly from your device.</strong><br />
  A local Vue PWA and Go backend for sunnypilot devices.
</p>

<p align="center">
  <a href="#try-it-locally">Quick start</a> ·
  <a href="#install-on-your-device">Install</a> ·
  <a href="docs/delivery.md">Updates</a> ·
  <a href="#development">Develop</a>
</p>

---

Dash turns your device's recordings into a searchable drive library. Connect over its hotspot or your LAN to review footage, follow the route, explore driving statistics, and download a whole session. The required **dash-backend** serves the PWA, API, recordings, and exports from one address. No login, cloud account, copyparty, or map token is needed.

| Review                                                     | Explore                                               | Take it with you                                                            |
| ---------------------------------------------------------- | ----------------------------------------------------- | --------------------------------------------------------------------------- |
| Thumbnails, search, sorting, and saved drives              | Playback-synchronized GPS map with Follow             | Full-session MP4 and original-video ZIP                                     |
| One timeline across recording parts, with camera switching | Recorded vehicle data and optional engagement overlay | ZIP of original assets                                                      |
| Timestamp entry to milliseconds and playback speed         | D3 dashboard with filters and a table alternative     | MCAP with `foxglove.CompressedVideo` and independent Dash telemetry schemas |

Desktop offers resizable library and video/map panels. Mobile keeps playback controls available across Video and Map tabs, with trip details in a sheet. Light, dark, and system themes include an opaque performance mode and reduced-motion support. Settings also offers English, 日本語, 简体中文, 한국어, Tiếng Việt, or Follow system; language changes apply immediately.

MCAP video uses H.264 with a keyframe in every message for paused Foxglove scrubbing. Exports preserve recorded frame timing and resolution, and include original files as attachments. This costs additional conversion time and storage; previously downloaded MCAPs need to be exported again.

Live view and bounded browser recording support firmware-managed WebRTC and a Dash-owned adapter for older sunnypilot builds with installed camera/media libraries. Both require a visible viewer and confirmed parked state. Leaving Live or losing the viewer stops capture. Metadata and statistics depend on the logs actually recorded; unavailable data stays unavailable.

HEVC cameras use short, cached H.264 previews while parked, with visible preparation feedback. Recorded AAC audio follows camera selection and stays in MP4 downloads. Follow remains enabled during GPS gaps, with a small availability indicator. Shelf’s dismissible notifications and eight accent presets apply throughout the interface; blue is the default.

## Try it locally

Start with synthetic recordings on your computer. This does not access your car.

**Build-host prerequisites**

| Tool             | Requirement                                                              |
| ---------------- | ------------------------------------------------------------------------ |
| Node.js + npm    | Node 22.12+ or a compatible newer release                                |
| Go               | 1.27.1+; see [go.mod](backend/go.mod)                                    |
| FFmpeg + ffprobe | 5+, including libx264 and libx265 for fixtures/tests                     |
| System tools     | Git, Make, C compiler for race tests, and bzip2 for legacy fixture tests |
| Python           | 3.9+ for installer/toolchain tests                                       |

On Linux/macOS, `bash scripts/setup-build-env.sh` installs or prepares host dependencies. See the [build-host guide](docs/delivery.md#build-yourself-and-debug-installation) for supported distributions and macOS prerequisites.

```sh
git clone git@github.com:LBYPatrick/dash.git
cd dash
make install
make build
make fixture
make serve
```

Open **<http://127.0.0.1:8088>**. The simulator contains three synthetic 68-second sessions with native `rlog.zst`/`qlog.zst`, segment rotation, 20 fps cameras and recorded-rate telemetry. It contains no captured road data. [Fixture provenance and regeneration](scripts/fixtures/README.md). Stop the server with Ctrl+C.

For frontend development, leave the backend running and run `make dev` in another terminal. Vite prints its development address and proxies `/api` and `/files` to the backend on port 8088. Without a backend, Dash offers a labeled sample library.

### Virtual device with Docker

```sh
make sandbox-up
make sandbox-test
```

Open **<http://127.0.0.1:8089>**. Choose **Parked** in the sandbox control to allow indexing; choose **Onroad** or **Unknown** to test worker lockout. The sandbox runs the Linux backend under enforced resource limits with synthetic recordings. It mounts no device data or SSH keys.

Use `make sandbox-logs` to follow logs and `make sandbox-down` to stop it. Docker is a development tool; devices use systemd. See [sandbox and diagnostics](docs/sandbox.md).

## Install on your device

Dash installs alongside sunnypilot over SSH. A firmware merge is not required. Installation, native recording playback and exports have been exercised on a physical sunnypilot device. Firmware camera/API compatibility can vary; see the [live-view requirements](docs/installation.md#firmware-live-view).

The target needs systemd, SSH access and noninteractive sudo for Dash’s service registration. Static FFmpeg/ffprobe are bundled; no device packages or Docker are needed. Installation requires confirmed parked state. The [installation guide](docs/installation.md) covers the complete requirements; the installer rejects unsupported configurations.

### From a release

Your computer needs this checkout, Make, SSH/SCP, and Python 3.9+. No local Node/Go build is needed.

```sh
make device-check DEVICE=comma@DEVICE_IP    # Read-only compatibility check
make device-install DEVICE=comma@DEVICE_IP # Latest main release
```

Choose another channel or pin a build:

```sh
make device-install DEVICE=comma@DEVICE_IP CHANNEL=nightly
make device-install DEVICE=comma@DEVICE_IP CHANNEL=dev RELEASE=TAG
```

The installer verifies the release, transfers the Go binary, prebuilt site and static media tools into `/data/dash`, then verifies systemd enablement and fresh supervised startup. A channel must have a published release before this route can work. Add `DEVICE_KEY=/path/to/key` when using a specific SSH key.

| Startup mode | Registration                                                | Survives a normal power cycle? |
| ------------ | ----------------------------------------------------------- | ------------------------------ |
| Default      | Physical unit in `/etc/systemd/system` with boot enablement | Yes                            |
| `RUNTIME=1`  | Temporary unit and enablement in `/run/systemd/system`      | No                             |

On AGNOS, persistent installation briefly remounts root to copy Dash’s small unit and enablement link, then restores read-only mode. The unit is readable before `/data` mounts; all app assets stay under `/data/dash`. A temporary install makes no system-partition writes and refuses an existing persistent registration. Reinstall without `RUNTIME=1` to promote a temporary install to boot startup.

On the device, while parked:

```sh
sudo bash /data/dash/verify-service.sh  # Unit, enablement and running backend
journalctl -u dash.service -f          # Startup and application logs
```

Open **`http://DEVICE_IP:8088`** from the same network. Trusted HTTPS is required to install the PWA on a LAN address; ordinary HTTP still serves the interface. See [HTTPS setup](docs/installation.md#local-https-and-pwa-installation).

### From your own source build

Build on a Linux/macOS computer and transfer the finished assets:

```sh
bash scripts/setup-build-env.sh
make device-install-source DEVICE=comma@DEVICE_IP
```

This route runs local checks, cross-compiles for the target, and uses the same systemd installer. Source builds are labeled `local` and have OTA disabled. `make source-release` creates an inspectable bundle without contacting a device. No compiler, Node, Python, Docker, or source checkout is installed on the device for Dash. [Source-install details →](docs/delivery.md#build-yourself-and-debug-installation)

## Processing and privacy

Heavy workers run only when firmware publishes **`IsOffroad=1`**. Older firmware’s `IsOnroad` flag also vetoes work whenever present and nonzero. Missing/invalid offroad state, unreadable flags and contradictory state block heavy work; a 100 ms watchdog cancels active work when state changes. Cached information and existing original downloads remain available. This is software cancellation, not a hard real-time guarantee.

One heavy operation runs at a time, only with confirmed parked state. Onroad or unknown state cancels processing. Fixed tunable constants in [`policy.go`](backend/policy.go) assume eight cores, allow up to eight media threads and set bounded Go/media allocations. Confirmed offroad uses the full fixed budget; onroad or unknown state reduces Go scheduling to one processor and blocks heavy work. There is no CPU quota or pinning, and device mode does not probe system resources.

Systemd requests 256 MiB memory high / 384 MiB maximum, zero swap and 128 tasks, and provides restart limits, journal logging and process-group cleanup. Available kernel enforcement varies by AGNOS; the app also uses soft memory targets and bounded buffers. See [resource policy and lifecycle](docs/installation.md#resource-policy). Indexing uses two bounded workers while parked, stops heavy work onroad/unknown, and resumes committed parts after interrupted power; see the [measured indexing improvements](docs/benchmarks/indexing.md). All installed assets and helpers live under `/data/dash`; `make device-uninstall DEVICE=comma@DEVICE_IP` removes them cleanly.

## Releases and updates

| Branch / channel | Purpose     |
| ---------------- | ----------- |
| `main`           | Stable      |
| `nightly`        | Preview     |
| `dev`            | Development |

Only commits to these three branches trigger CI and publish release artifacts. Feature branches and PR updates trigger no CI; work on a feature branch targeting `dev`, then promote through `nightly` to `main`.

Managed builds use Settings to check, download, and restart into an update while parked. OTA requires internet, verifies checksums, and uses a systemd-supervised bootstrap with startup rollback. Local source builds update through SSH instead. [Artifacts, tags, trust, and OTA details →](docs/delivery.md)

**After AGNOS updates:** `/data/dash` normally persists, including the bundled media tools; the systemd unit and enablement on the OS partition may be replaced. While parked, run `sudo bash /data/dash/install.sh`, then `sudo bash /data/dash/verify-service.sh`. Dash does not patch firmware boot scripts to reinstall itself. A factory reset or data wipe can remove Dash. See [AGNOS recovery and removal](docs/installation.md#agnos-updates-and-removal).

## Development

```sh
make format       # Prettier and gofmt
make check        # Formatting, vet, installer tests, frontend/Go race tests, build
make build-arm64  # Production PWA and Linux ARM64 binary
make help         # Available workflows
```

With `make serve` running against `make fixture`, run `make smoke` for browser integration tests. Chrome is required; set `CHROME_PATH` if needed. These tests change synthetic params and write artifacts under ignored `.artifacts/`. Specialized viewer, map, statistics, mobile, appearance, and motion checks are listed by `make help` and explained in the audit guides.

```text
frontend/        Vue PWA: src/, public/, package.json, Vite and TypeScript config
backend/         Go service, telemetry schemas, exports, OTA and systemd helpers
docs/            Installation, delivery, sandbox and maintained design guides
scripts/         Shared host setup, SSH installation, release and integration tests
tools/, docker/  Static media builds and development sandbox
Makefile         Shared development and delivery commands
package.json     npm workspace, shared test/format/browser tools and release version
package-lock.json  One lockfile for the workspace
```

Run npm and Make commands from the repository root; `make build` writes the PWA to `frontend/dist` and the host binary to `.artifacts/dash-backend`. The installed release layout remains `dash-backend` plus `dist/`.

Keep secrets in ignored `.env` files; never commit credentials, device keys, or private recordings. Run `make check` locally before proposing a merge. Browser tests and cross-builds do not certify device safety or physical Safari behavior.

## Guides

- [Installation, systemd, HTTPS, and recovery](docs/installation.md)
- [Delivery channels, source builds, and OTA](docs/delivery.md)
- [Docker sandbox and diagnostics](docs/sandbox.md)
- [Maps and tile-service policy](docs/maps.md)
- [Languages and translation customization](docs/i18n.md)
- [Telemetry and MCAP format](backend/SCHEMA.md)
- [UI component audit](docs/ui-audit.md) · [Mobile and motion audit](docs/mobile-motion-audit.md)

Shelf-derived components retain their [upstream license notice](LICENSES/Shelf.txt).
