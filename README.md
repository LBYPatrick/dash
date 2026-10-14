<p align="center">
  <img src="public/icon.svg" alt="Dash" width="80" />
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

Live view and bounded browser recording are available **only on compatible firmware with managed WebRTC support**. Metadata and statistics depend on the logs actually recorded; unavailable data stays unavailable.

## Try it locally

Start with synthetic recordings on your computer. This does not access your car.

**Build-host prerequisites**

| Tool             | Requirement                                                  |
| ---------------- | ------------------------------------------------------------ |
| Node.js + npm    | Node 22.12+ or a compatible newer release                    |
| Go               | 1.27.1+; see [go.mod](dash-backend/go.mod)                   |
| FFmpeg + ffprobe | 5+, including libx264 and libx265 for fixtures/tests         |
| System tools     | Git, Make, C compiler for race tests, and bzip2 for fixtures |
| Python           | 3.9+ for installer/toolchain tests                           |

On Linux/macOS, `bash scripts/setup-build-env.sh` installs or prepares host dependencies. See the [build-host guide](docs/delivery.md#build-yourself-and-debug-installation) for supported distributions and macOS prerequisites.

```sh
git clone git@github.com:LBYPatrick/dash.git
cd dash
make install
make build
make fixture
make serve
```

Open **<http://127.0.0.1:8088>**. The fixture contains synthetic video and telemetry, not captured road data. Stop the server with Ctrl+C.

For frontend development, leave the backend running and run `make dev` in another terminal. Vite prints its development address and proxies `/api` and `/files` to the backend on port 8088. Without a backend, Dash offers a labeled sample library.

### Virtual device with Docker

```sh
make sandbox-up
make sandbox-test
```

Open **<http://127.0.0.1:8089>**. Choose **Parked** in the sandbox control to allow indexing; choose **Onroad** or **Unknown** to test worker lockout. The sandbox runs the Linux backend under enforced resource limits with synthetic recordings. It mounts no device data or SSH keys.

Use `make sandbox-logs` to follow logs and `make sandbox-down` to stop it. Docker is a development tool; devices use systemd. See [sandbox and diagnostics](docs/sandbox.md).

## Install on your device

Dash installs alongside sunnypilot over SSH. A firmware merge is not required. **Physical comma-device installation and live camera compatibility have not yet been validated.** Check your actual firmware and AGNOS image before deploying.

The target needs systemd, unified cgroup v2 with CPU/memory/PID controllers, FFmpeg 5+ with the required encoders/filters, SSH access, and noninteractive sudo. Installation requires confirmed parked state. The [installation guide](docs/installation.md) covers the complete requirements; the installer rejects unsupported configurations.

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

The installer verifies the downloaded release, transfers the Go binary and prebuilt site, installs under `/data/dash`, enables `dash.service`, and verifies the live kernel resource limits. A channel must have a published release before this route can work.

Open **`http://DEVICE_IP:8088`** from the same network. Trusted HTTPS is required to install the PWA on a LAN address; ordinary HTTP still serves the interface. See [HTTPS setup](docs/installation.md#local-https-and-pwa-installation).

### From your own source build

Build on a Linux/macOS computer and transfer the finished assets:

```sh
bash scripts/setup-build-env.sh
make device-install-source DEVICE=comma@DEVICE_IP
```

This route runs local checks, cross-compiles for the target, and uses the same systemd installer. Source builds are labeled `local` and have OTA disabled. `make source-release` creates an inspectable bundle without contacting a device. No compiler, Node, Python, Docker, or source checkout is installed on the device for Dash. [Source-install details →](docs/delivery.md#build-yourself-and-debug-installation)

## Processing and privacy

Heavy workers run only when firmware explicitly reports **`IsOnroad=0` and `IsOffroad=1`**. Onroad, unknown, missing, or contradictory state blocks them; a 100 ms watchdog cancels active work when state changes. Cached information and existing original downloads remain available. This is software cancellation, not a hard real-time guarantee.

One heavy operation runs at a time. The installed service enforces these limits across Dash **and its child processes**:

| Resource        | Service limit                                        |
| --------------- | ---------------------------------------------------- |
| CPU             | 50% of one core                                      |
| Memory          | 256 MiB pressure threshold; 384 MiB hard maximum     |
| Swap / tasks    | No swap; 64 tasks                                    |
| Low-memory gate | Heavy work pauses below 512 MiB device available RAM |

Go also has a 128 MiB soft memory target; FFmpeg has a monitored 192 MiB RSS ceiling. Firmware-owned WebRTC processes are outside Dash's cgroup. Systemd provides restart limits, journal logging, and process-group cleanup. See [resource controls and lifecycle](docs/installation.md#verified-limits).

Recordings are read-only. SQLite indexes, previews, and exports are disposable cache. Rebuild the index in Settings; download bounded backend logs through the device card's **Download backend diagnostics** action.

Dash has **no authentication**: anyone who can reach its port can access recordings and request parked work. Keep it on a trusted hotspot/LAN. Maps load OpenStreetMap tiles automatically and require internet for their background; the tile provider sees the requested area. GPS overlays remain available when tiles fail. The PWA caches its shell, not footage, exports, or map tiles. [Map configuration and usage policy →](docs/maps.md)

## Releases and updates

| Branch / channel | Purpose     |
| ---------------- | ----------- |
| `main`           | Stable      |
| `nightly`        | Preview     |
| `dev`            | Development |

Only commits to these three branches trigger CI and publish release artifacts. Feature branches and PR updates trigger no CI; work on a feature branch targeting `dev`, then promote through `nightly` to `main`.

Managed builds use Settings to check, download, and restart into an update while parked. OTA requires internet, verifies checksums, and uses a systemd-supervised bootstrap with startup rollback. Local source builds update through SSH instead. [Artifacts, tags, trust, and OTA details →](docs/delivery.md)

**After AGNOS updates:** `/data/dash` persists, but the systemd unit, enablement, and OS-installed FFmpeg may need restoration. Dash OTA does not restore OS dependencies. Follow [AGNOS recovery](docs/installation.md#agnos-updates-and-removal) before restarting Dash.

## Development

```sh
make format       # Prettier and gofmt
make check        # Formatting, vet, installer tests, frontend/Go race tests, build
make build-arm64  # Production PWA and Linux ARM64 binary
make help         # Available workflows
```

With `make serve` running against `make fixture`, run `make smoke` for browser integration tests. Chrome is required; set `CHROME_PATH` if needed. These tests change synthetic params and write artifacts under ignored `.artifacts/`. Specialized viewer, map, statistics, mobile, appearance, and motion checks are listed by `make help` and explained in the audit guides.

```text
src/             Vue interface, playback, maps, and dashboard
public/          PWA icons, map configuration, and sample media
dash-backend/    Go service, index, telemetry, exports, OTA, and systemd helpers
scripts/         Host setup, SSH installation, packaging, and browser checks
docs/            Installation, delivery, sandbox, and UI guides
```

Keep secrets in ignored `.env` files; never commit credentials, device keys, or private recordings. Run `make check` locally before proposing a merge. Browser tests and cross-builds do not certify device safety or physical Safari behavior.

## Guides

- [Installation, systemd, HTTPS, and recovery](docs/installation.md)
- [Delivery channels, source builds, and OTA](docs/delivery.md)
- [Docker sandbox and diagnostics](docs/sandbox.md)
- [Maps and tile-service policy](docs/maps.md)
- [Languages and translation customization](docs/i18n.md)
- [Telemetry and MCAP format](dash-backend/SCHEMA.md)
- [UI component audit](docs/ui-audit.md) · [Mobile and motion audit](docs/mobile-motion-audit.md)

Shelf-derived components retain their [upstream license notice](LICENSES/Shelf.txt).
