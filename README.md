# Dash

A local drive library for sunnypilot devices. A Vue PWA and a required Go service, **dash-backend**, serve the interface, recordings, index, and exports from one address. No login, copyparty, cloud account, or Python runtime for Dash. Device installation uses systemd; Docker is optional for development.

Open Dash while parked, choose a drive, scrub one recording timeline, switch cameras, and export the complete session. The interface works on a phone connected to the device hotspot or another computer on the same LAN.

## Included

- Glass controls with restrained corners, readable text, sliding selection highlights, animated menus/sheets, and system typography. Settings offers Light / Dark / System themes and an opaque performance mode; preferences persist per browser. Reduced motion, reduced transparency and increased contrast preferences are respected; effects render on the client.
- Searchable, sortable drive library with thumbnails and browser-local bookmarks.
- Incremental SQLite indexing, disposable cache recovery, and a rebuild action. Original recordings are read-only.
- A viewport-sized session viewer with camera footage beside the map/metadata on desktop, a draggable divider that remembers the panel widths, and Video / Map & data tabs on mobile. Downloads and bookmarks live in the three-dot actions menu. Overview, Recordings, Live and device switching sit in the desktop top bar or mobile navigation drawer.
- Continuous, seekable HLS playback across recording files, with automatic transitions and one selected camera at a time. Missing or unreadable camera files are omitted.
- Backend full-session ZIP, MP4, raw-video ZIP, and MCAP exports. No segment selection; the backend discovers every session part. Raw-video ZIP preserves the selected camera’s original files without conversion. MP4 can retain HEVC or convert to H.264 for compatibility. MCAP uses Dash's own JSON schemas and stores original recordings as attachments; it does not provide frame-level video channels.
- GPS route, measured distance, recorded duration, vehicle/version metadata, and library overview when supported logs are present. Missing metadata stays missing. Maps load automatically using OpenStreetMap, or Connect’s Mapbox Light / Dark v11 styles when configured; the GPS route remains visible when maps are unavailable. Rendering runs in the browser. Copy `.env.example` to ignored `.env` and set `VITE_MAPBOX_TOKEN` (optionally `VITE_MAPBOX_STYLE`) for Mapbox at build time. No token is embedded in source or Git history. Browser map tokens are public in compiled frontend assets; keep private credentials out of `VITE_*` variables. Docker excludes local env files and uses OpenStreetMap by default.
- Parked-only local WebRTC on firmware with the managed `IsLiveStreaming` API. Browser recording is available where MediaRecorder supports the negotiated stream.
- Installable PWA shell over trusted HTTPS. Recordings and exports are not cached offline.

## Run locally

Requires Node 22.12+ (or a compatible newer release), Go 1.27.1+, and FFmpeg/ffprobe with H.264 encoding support.

```sh
make install
make build
make fixture
make serve
```

Open <http://127.0.0.1:8088>. The fixture contains **synthetic** illustrated video and generated telemetry, not real driving data. Without a connected backend, the UI offers a clearly labeled sample library.

For frontend development, keep the backend running and run `make dev`; Vite proxies `/api` and `/files`. To use actual local recordings, launch the binary with explicit `--root`, `--params`, `--cache`, and `--app` paths. Missing driving-state params disable processing.

## Verify without a device

```sh
make sandbox-up
make sandbox-test
```

Open <http://127.0.0.1:8089> and select Parked in the sandbox control to index synthetic recordings. Select Onroad or Unknown to verify worker lockout. The sandbox uses the actual Linux backend with enforced Docker CPU/memory/PID limits, without mounting device data or SSH keys. See [sandbox and diagnostics](docs/sandbox.md) for details and limits of the simulation.

Backend diagnostics are downloadable from the device card in the frontend, including while onroad. Reports include bounded recent logs and runtime status; they exclude footage and GPS samples.

## Install on a device

Dash installs alongside sunnypilot through SSH; you do not need to merge it into sunnypilot. A firmware distributor can package the same service and assets. The device must meet the requirements in [installation and operations](docs/installation.md), especially **unified cgroup v2 with CPU, memory, and PID controllers**. Unsupported configurations are rejected rather than running without enforced limits.

From this repository on your development computer:

```sh
make device-check DEVICE=comma@DEVICE_IP    # Read-only SSH preflight
make device-install DEVICE=comma@DEVICE_IP  # Build, upload, install, verify
```

You can also invoke `bash scripts/install-backend.sh [--check] comma@DEVICE_IP` directly. Installation requires confirmed parked state and rechecks it before activation. The installer retains previous assets and restores them if service verification fails, keeping the service stopped.

The installer cross-compiles Go for the detected Linux architecture, uploads the PWA and binary into `/data/dash`, installs `dash.service`, enables boot startup, and checks the live kernel controls. It requires SSH key access and noninteractive sudo. Open `http://DEVICE_IP:8088` from the same network. Trusted HTTPS is needed for service-worker installation on a LAN address; see the installation guide.

## Driving and resource policy

Heavy processing is permitted only when firmware params explicitly report `IsOnroad=0` and `IsOffroad=1`. Onroad, missing, invalid, or contradictory state blocks workers. A 100 ms watchdog cancels indexing, exports, conversion, and live sessions when that condition changes. The HTTP service remains available for existing cached information and original downloads. This is a software cancellation policy, not a hard real-time guarantee; firmware state accuracy and scheduling still matter.

One heavy operation runs at a time. The installed systemd service caps the **whole Dash cgroup, including FFmpeg**, at 50% of one CPU core, 384 MiB memory, no swap, and 64 tasks. It applies memory pressure at 256 MiB. Go has a 128 MiB soft memory target, FFmpeg a monitored 192 MiB RSS ceiling, and work pauses when device available memory falls below 512 MiB. Kernel enforcement is verified by the backend before workers start and by the installer after startup. Firmware-owned WebRTC processes are outside Dash's cgroup; Dash requests their shutdown through the managed firmware API.

See [operations and persistence](docs/installation.md) for lifecycle, logs, update recovery, and limitations; [SCHEMA.md](dash-backend/SCHEMA.md) describes telemetry and MCAP semantics.

## Verification

```sh
make check          # formatting, Go vet, frontend tests, Go race tests, builds
make build-arm64    # Linux ARM64 cross-build
# With make serve running against make fixture:
make smoke          # Chrome browser integration tests
```

Set `CHROME_PATH` if Chrome is not at the macOS default path. Smoke tests exercise continuous playback and seeking across file boundaries, camera switching, panel dragging/reset/keyboard resizing, metadata, search/bookmarks, all export formats, onroad refusal, mobile layout, and the offline PWA shell. They modify only synthetic fixture params and write screenshots/downloads under ignored `.artifacts/`. `make design-smoke` additionally checks five viewport sizes, control fit, drawer focus and reopening, and reduced motion/transparency with high contrast. `make shelf-smoke` checks shared popup/menu accessibility, keyboard focus, content sizing and mobile overflow; see [the component and flow audit](docs/ui-audit.md). `make appearance-smoke` runs rendered contrast audits in both themes, with and without performance mode, and verifies persistence, System theme changes, cross-tab sync and stable scrollbar gutters.

Backend tests cover independent cereal fixtures, malformed logs/index recovery, ignition cancellation of a running FFmpeg process, queued-export cancellation, MCAP contents, HTTP path boundaries, and fail-closed resource checks. Local checks and cross-compilation do **not** certify a specific comma device, AGNOS release, or live camera stream. No physical device deployment has been performed for this implementation.
