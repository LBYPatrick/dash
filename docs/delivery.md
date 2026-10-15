# Delivery channels and updates

Dash follows Shelf's check → available → download → ready → restart flow. There are three independent delivery channels; no account, PAT, signing secret, compiler, Git checkout or JavaScript toolchain is installed on the device.

| Branch    | Channel     | Release tag example           | GitHub classification |
| --------- | ----------- | ----------------------------- | --------------------- |
| `main`    | Stable      | `v0.1.0+main.123.abcdef01`    | Release, latest       |
| `nightly` | Preview     | `v0.1.0-nightly.124.abcdef01` | Prerelease            |
| `dev`     | Development | `v0.1.0-dev.125.abcdef01`     | Prerelease            |

Work on `feat/*` branches targeting `dev`. Promote reviewed work from `dev` to `nightly`, then `main`. The channel name `nightly` is not a cron schedule: each channel builds on commits. `.github/workflows/channels.yml` listens **only** for pushes to these three branches. Opening/updating a feature PR, pushing a feature branch, or pushing a tag starts no workflow. A merged PR pushes its result to the target channel and triggers delivery. Direct channel commits have the same behavior. Run `make check` locally before requesting a merge; do not require pre-merge workflow statuses that this policy intentionally does not produce.

The workflow runs formatting, Python installer tests, JS tests, Go race tests/vet, the production frontend/native build, a Docker OTA activation/rollback exercise under enforced device resource limits, and the actual source installer with a mocked SSH/SCP transport. It then cross-compiles Linux ARM64 and AMD64 with `CGO_ENABLED=0` and packages the same prebuilt frontend and a matching static FFmpeg/ffprobe bundle into each archive. Docker Buildx/QEMU compile and exercise the pinned media sources on CI; no build container runs on the device. CI needs no repository secrets: only its short-lived `github.token`, with release-writing permission confined to the publish job. Action revisions are pinned. GitHub supports `concurrency.queue: max`; actionlint v1.7.12 still flags that new property, so workflow structure tests also verify the channel-only trigger and queue policy. Per-channel runs queue rather than cancel each other (GitHub's queue limit is 100 pending runs).

## Release assets

Each immutable build release contains:

- `dash-<safe-tag>-linux-arm64.tar.gz`
- `dash-<safe-tag>-linux-amd64.tar.gz`
- `manifest.json` identifying protocol, version, channel, source revision, CI build number, artifact sizes and SHA-256 digests
- `SHA256SUMS`

`<safe-tag>` is the release tag with `+` replaced by `-` for GitHub asset filenames; the actual release tag and version keep `+`. An archive contains `dash-backend`, `dist/`, `release.json`, and `bin/` with static FFmpeg/ffprobe, checksums, licenses and exact corresponding source/build files. Media binaries are checked for Linux architecture and static linkage during extraction. `dist/` is the production PWA, not source or `node_modules`. The binary's compiled identity and the archive metadata agree. `package.json` supplies the base version; the workflow run number and source hash make every channel build unique without a manual version bump for every commit. Increase the base version for product releases. Keep this workflow's build-number history; recreating the workflow with a lower counter requires a deliberate migration rather than silently downgrading devices.

Publishing follows Shelf's all-or-nothing approach: draft the build release, upload the complete artifact set, then publish. Published builds are never overwritten. Retrying a failed unpublished draft may fill its assets. If publication succeeded but updating the feed failed, a retry verifies the published manifest and resumes only the feed step. After publication, CI advances `manifest.json` on the corresponding `channel-main`, `channel-nightly`, or `channel-dev` feed release. These three prerelease tags are **fixed feed locators**, not moving source tags; the manifest identifies the immutable build/source to use. A brief 404 while replacing a feed asset is a recoverable check failure. An old rerun cannot move a feed backwards.

A fresh repository has no downloadable channel until its first successful channel workflow. Feature branches can exercise packaging and OTA locally without publishing anything:

```sh
make check
make release CHANNEL=dev BUILD=1
make sandbox-up
make ota-smoke
```

`make ota-smoke` builds two local CI-format versions, boots the real Linux supervisor/backend in Docker, applies an update, checks persistence and limits, rejects installation with unknown driving state, then stages a deliberately failing ELF to prove rollback. It injects local verified-stage fixtures; it does not create GitHub releases or access a car. Unit tests separately exercise HTTPS feed handling, checksum/download boundaries, archive rejection and ignition cancellation. Local fixtures are not official CI releases.

## Installation from an SSH client

```sh
# Read-only device requirements; no download, build, upload or service change.
make device-check DEVICE=comma@DEVICE_IP

# Latest successful main release, the default.
make device-install DEVICE=comma@DEVICE_IP

# Other channels, or a pinned immutable build in that channel.
make device-install DEVICE=comma@DEVICE_IP CHANNEL=nightly
make device-install DEVICE=comma@DEVICE_IP CHANNEL=dev RELEASE=v0.1.0-dev.125.abcdef01

# Equivalent script options; a public fork can supply --repo OWNER/REPO.
bash scripts/install-backend.sh --channel main --release v0.1.0+main.123.abcdef01 comma@DEVICE_IP
```

The client needs SSH/SCP and Python 3.9+ standard library. It defaults to ARM64 (`--arch amd64` is available for compatible test hosts), fetches the channel manifest (or exact release manifest), verifies the complete archive's size/digest, safely extracts it locally, then transfers **prebuilt files**. No local npm/Go build is part of installation. The device needs the systemd/permissions/CA prerequisites in [installation.md](installation.md), and confirmed parked state both before staging and before activation. Media tools update atomically with the release; OS packages are never installed or updated by Dash.

## Build-yourself and debug installation

This is a separate **host build → verified payload → SSH transfer → systemd activation** route. It requires no GitHub release or CI. Docker is required on the **build host** to compile the pinned static media bundle; verified local build outputs are reused. Runtime has no Docker dependency. Use it for an unpublished feature branch or local changes; nothing is built on the driving device.

```sh
# Clone on your Linux/macOS workstation or build box, then enter the checkout.
git clone git@github.com:LBYPatrick/dash.git
cd dash
bash scripts/setup-build-env.sh   # Works even before make is installed
make build-env-check

# Read-only device preflight; builds/uploads nothing.
bash scripts/install-backend.sh --source --check comma@DEVICE_IP

# Full local checks/build, verified ARM64/AMD64 bundle, SSH/SCP and systemd.
make device-install-source DEVICE=comma@DEVICE_IP
# Equivalent:
bash scripts/install-backend.sh --source comma@DEVICE_IP

# Inspect/package locally without contacting any device.
make source-release
```

Host setup supports macOS (Apple Silicon/Intel, Command Line Tools already installed) and Debian/Ubuntu Linux (ARM64/AMD64; Ubuntu 24.04+ recommended for FFmpeg 5+). macOS reuses compatible installed tools, using Homebrew for missing Python/FFmpeg; install [Homebrew](https://brew.sh) first if needed. Linux setup uses apt and sudo for Python, build-essential, make, git, SSH, CA certificates, bzip2 (synthetic fixture tooling) and FFmpeg. Other Linux distributions can install those system prerequisites with their own package manager and run the check/build targets manually. Python must be 3.9+; Go must meet `dash-backend/go.mod`, and Node must be at least 22.12 with npm.

Compatible existing Go/Node installations are reused. Missing/incompatible SDKs are downloaded from official [Go](https://go.dev/doc/install) and [Node](https://nodejs.org/en/download) distribution hosts, SHA-256 verified against their HTTPS metadata, and installed under ignored `.artifacts/toolchains/`. No shell profile is edited and no downloaded installer script is executed. Make targets include the local SDK paths; for direct shell commands run `source .artifacts/toolchains/env.sh`. Setup installs npm/Go project dependencies. `make build-env-check` is read-only and makes no network requests. The setup script refuses to run on a detected driving device. Start Docker before building a device release; `make build-media MEDIA_ARCH=arm64` builds and caches its static tools. Docker also powers the separate virtual-device tests.

The source installer performs the same read-only device preflight before building, runs `make check`, builds the matching production PWA, and cross-compiles a pure-Go Linux binary for the device architecture. Archives use `v0.1.0-local.<timestamp>.<revision>[.dirty]` identities; `.dirty` records uncommitted source changes. Only the binary, static site, bundled media tools/sources/licenses and build metadata are transferred, after local size/digest/path/architecture checks identical to the release route. It rechecks parked state before transfer and activation, installs the same root-owned bootstrap/unit, waits for service health and verifies fresh supervised startup and the app’s fixed resource policy. Recordings and cache stay in place. SSH authentication and passwordless sudo must already work; configure keys/known-host trust normally on the host (or use an SSH config alias such as `comma@my-comma`). Private keys are never copied into payloads. No SSH keys or driving params are changed. On a read-only AGNOS root, only Dash’s small unit copy and enablement link require a brief writable remount, restored immediately by a trap. `--runtime` / `RUNTIME=1` avoids system-partition writes for a temporary session; that mode does not survive a reboot.

Source builds show their local identity in Settings and **cannot check/download/apply OTA**, even if an earlier release enabled automatic checks. `--source` rejects channel/release/repository options so it cannot masquerade as a channel build. Re-run the source installer to deploy a fix. To return to OTA-managed stable builds, run `make device-install DEVICE=comma@DEVICE_IP`; choose `CHANNEL=dev` or `CHANNEL=nightly` if appropriate.

`make source-install-smoke` runs the actual source installer/build with a mocked SSH/SCP transport, checking the transferred ARM64 payload, disabled-OTA configuration and systemd activation script without contacting a device. The host SDK extractor has offline checksum/path/symlink tests in `make check`.

Both sunnypilot and openpilot offer SSH access, but that alone does not prove a particular firmware/AGNOS image meets Dash's requirements. See [comma's connection instructions](https://docs.comma.ai/how-to/connect-to-comma/) and [sunnypilot's developer settings](https://community.sunnypilot.ai/t/developer-settings/3699). Unknown/onroad state fails preflight. Device mode uses fixed budgets rather than probing resource controllers or CPU topology. This route never patches firmware or changes driving params.

## Installed layout and bootstrap

```text
/data/dash/
  dash-bootstrap           root-owned stable Go bootstrap, installed over SSH
  boot-state.sh            boot-only firmware state freshness guard
  current -> releases/<tag>
  previous -> releases/<last healthy tag>
  releases/<tag>/
    dash-backend           replaceable Go service
    dist/                  matching prebuilt PWA
    bin/                   static FFmpeg/ffprobe, manifest, licenses, sources
    release.json
  update.json              repository, selected channel, automatic checks
  ready.json               verified downloaded manifest, when present
  pending.json             explicit restart/install request, when present
  activation.json          durable activation journal during probation
  update-failure.json      rollback/postponement explanation, when present
  cache/                   existing index, previews, exports and bounded logs
    supervisor-ready.json  live bootstrap/child identity after health probation
  install.sh / uninstall.sh / verify-service.sh
  systemd-registration.sh  root-owned scoped unit setup/restoration
  systemd/dash.service     persistent source; small copy registered in /etc
```

A systemd-owned boot guard waits for fresh firmware state before executing the stable bootstrap as the main process. Its `/run/dash` markers survive same-boot restarts and disappear at reboot. It starts the selected backend directly, with no shell or detached worker, in its own process group inside the **same** service cgroup. Device mode applies fixed constants: a 24 MiB bootstrap heap target, 96 MiB backend target, one heavy job and an eight-core/media-thread ceiling, without CPU quota or affinity. Systemd requests aggregate memory high 256 MiB/max 384 MiB, zero swap and 128 tasks; kernel support varies and is not probed. Startup/restart limits, journal logging and whole-cgroup shutdown remain systemd's responsibility. The supervisor kills the retired child's entire process group before replacement, including any surviving FFmpeg child. After startup probation, it sleeps waiting for process exit rather than polling while driving.

App updates cannot edit systemd units or invoke sudo. The stable bootstrap/protocol and root-owned unit helpers change only through an explicit SSH installation. This is intentional bootloader separation: an app update cannot replace its rollback mechanism. A future protocol or unit migration needs a new SSH install. AGNOS persistence/recovery follows [installation.md](installation.md): `/data` persists normally, `/etc` unit registration and OS packages may not.

## Updating in Settings

Managed installations check automatically after the first parked minute and then at most every six hours. Offline checks fail quietly into the Settings state; there is no popup or retry loop while driving. Automatic checks can be disabled. They never automatically download or restart the app.

Settings → Dash updates opens a dedicated Shelf popup showing the current build and delivery channel. Choose a channel, Check for updates, Download update, then Restart Dash and install. Closing either popup preserves an active/completed backend download and post-install health tracking. A failed status fetch has an explicit retry; an older poll cannot revert a newly saved channel. A completed download survives page, service and device restarts. Explicit channel changes can select an older build in a different channel; within a channel, automatic discovery ignores stale/lower build numbers.

All network checks, downloads, hashing, extraction and cleanup run only while confirmed parked, under the shared compute slot. Unknown/onroad states reject new operations and cancel ongoing transfers/extraction with the existing 100 ms watchdog. A canceled stage is left for cleanup at the next parked attempt; no recursive cleanup continues on the road. No update deletes recordings. Downloads require 2 GiB free space; compressed archives are capped at 128 MiB, expanded content at 256 MiB and 2,048 entries. Traversal, symlinks, special files, duplicate paths, incorrect architecture/identity and missing frontend builds are rejected. Old inactive release directories are pruned only while parked; current, previous and the requested candidate are retained.

Installation rechecks parked state after the old server drains. If it changed, the existing build restarts and reports postponement; choose Install again when parked. Applying a candidate writes and syncs the activation journal before atomically switching `current`. The new child must bind its listener, report its actual PID/revision/tag, and stay alive for ten seconds within a 45-second deadline. Startup verifies bundled tool availability, logging/index initialization and the matching PWA. Failure restores the previous link and starts that version. If power fails before the health transaction commits, next boot restores the recorded previous release. A stable backend failure without an update request is handed back to systemd's existing restart limits.

The browser reconnects and reloads after the new backend completes health probation; the service worker is refreshed where available. Insecure tethered HTTP works without a service worker. A rollback explanation is available in Settings and backend diagnostics. This startup check is not a substitute for testing actual comma hardware or firmware compatibility.

## Trust and network behavior

As with Shelf's Linux update flow, trust is the public GitHub repository over system-verified HTTPS and the manifest's archive digest. This is **not independent publisher-signature verification**; repository release writers are trusted to ship code. No PAT or private signing key is installed on a device. Only GitHub and its designated release-asset hosts are permitted as download/redirect targets; archive addresses are derived from the validated repository/tag/asset name. Arbitrary URLs cannot be supplied by the browser. Public forks require explicit installer/configuration selection. Private repositories need a separate authenticated distribution design and are not supported by this anonymous feed.

Dash updates only its own release assets, including its bundled media tools. It never modifies sunnypilot, driving params, system FFmpeg or firmware startup. Firmware-owned live flags keep their existing narrowly scoped behavior. Test reports distinguish host/container checks from physical-device acceptance.
