# Installation and operations

## Requirements

The target needs a comma 3X/4 running Linux ARM64, systemd, a `comma` user/group, readable recordings and driving params, writable `/data`, and SSH with noninteractive sudo for Dash's service registration. Use a confirmed parked device. Dash requires no device package installs, Docker, Node, Go toolchain, or system FFmpeg. Recordings and exports run in Go with bundled media tools. The optional legacy live adapter reuses the Python, PyAV, aiortc, numpy and VisionIPC libraries already installed by supported firmware; Dash does not install them. Releases include static FFmpeg/ffprobe with their licenses, source archives and build recipe.

Enable device SSH using the [official connection instructions](https://docs.comma.ai/how-to/connect-to-comma/). Use the address shown on your current network; no network switching is necessary. The PWA and API share `http://DEVICE_IP:8088`. Once installed, recordings and exports require no internet or account.

All app assets live in `/data/dash`. The only persistent files outside it are a small systemd unit copy at `/etc/systemd/system/dash.service` and its boot-enablement symlink. A unit symlink into `/data` cannot reliably start at boot because systemd reads units before mounting `/data`; the copied unit declares `RequiresMountsFor=/data`. On AGNOS, root is normally read-only. The root-owned registration helper briefly remounts it writable **only** to add/remove Dash’s unit and enablement link, then restores read-only state on success or failure. It never edits firmware launchers, other units or mount configuration. Firmware and original recordings are left in place. Device mode uses fixed app budgets and does not inspect CPU topology, RAM availability, `/proc` or cgroup controller files.

## SSH installation from your computer

```sh
make device-check DEVICE=comma@DEVICE_IP
make device-install DEVICE=comma@DEVICE_IP
```

The check is read-only: it verifies parked state, recording permissions and that any existing `dash.service` belongs to Dash. Installation downloads and checksum-verifies the latest published `main` release on your computer, including the Go backend, PWA and matching static media tools. It transfers the payload, rechecks parked state, copies the boot-readable systemd unit, enables it and verifies fresh supervisor/backend readiness. A failed activation restores the previous release when available and leaves Dash stopped. The SSH client needs Go matching `backend/go.mod` to run the host-side verifier. No Go toolchain is installed on the device.

For a temporary test without any system-partition writes, add `RUNTIME=1` (`--runtime` in the script). Registration then lives in `/run`, disappears at reboot and does **not** provide boot startup. Persistent registration is the default. A runtime request is refused if persistent registration already exists, before downloads or service changes. To promote a temporary install, reinstall without `RUNTIME=1`; the helper publishes the persistent unit before removing its temporary registration. To return to temporary-only operation, uninstall first, then install with `RUNTIME=1`.

Add `CHANNEL=nightly|dev`, `RELEASE=TAG`, or `DEVICE_KEY=/path/to/key`. ARM64 is the default; the script accepts `--arch amd64` for a compatible Linux test host. SSH keys stay on your computer.

For unpublished code, run `make setup-build-env`, start Docker on the **build host**, then `make device-install-source DEVICE=comma@DEVICE_IP [DEVICE_KEY=/path/to/key]`. Compilation, tests and static media builds run on Linux/macOS; only finished assets go to the car. `make source-release` packages without contacting a device. Local builds disable OTA. See [delivery and OTA](delivery.md).

## Files and startup

`/data/dash/dash-bootstrap` supervises `/data/dash/current/dash-backend`, which serves `/data/dash/current/dist`, reads `/data/media/0/realdata`, keeps disposable files in `/data/dash/cache`, and observes `/data/params/d`. It detects firmware capabilities in `/data/openpilot`. Use `/data/dash/dash.env` for optional `DASH_TLS_ARGS` or `DASH_EXTRA_ARGS`; keep customization inside the app folder. The unit source and installation helpers remain in `/data/dash`.

```sh
sudo systemctl status dash.service
sudo systemctl stop dash.service
sudo systemctl start dash.service
sudo systemctl restart dash.service
journalctl -u dash.service -f
sudo bash /data/dash/verify-service.sh
```

The verification helper checks that the loaded unit is a physical copy matching `/data/dash/systemd/dash.service`, its enablement link resolves to that copy, the boot guard validated fresh state, and the running supervisor/backend match the current release. A responsive HTTP port alone is insufficient.

Boot uses `WantedBy=multi-user.target`; `/data` must be mounted first. There is no `nohup`, tmux daemon launcher, or Docker requirement. `Restart=on-failure`, a 10-second delay, and a limit of three starts per five minutes prevent tight restart loops. After fixing a persistent failure:

```sh
sudo systemctl reset-failed dash.service
sudo systemctl start dash.service
```

Use the frontend device card’s **Download backend diagnostics** action to collect bounded recent application logs and runtime/resource status. Reports exclude footage and GPS samples; review error paths before sharing. Logs persist under `/data/dash/cache/logs` with a maximum of three 2 MiB files. If the backend cannot start, use the journal instead.

Logs go to the journal; journal retention/persistence follows the device's journald configuration. `KillMode=control-group` stops the backend and all its child processes; after five seconds systemd kills survivors. `OOMPolicy=stop` stops the unit following an OOM event. `ExecStopPost` clears a Dash-owned live request even after backend failure. The legacy adapter and its camera child belong to Dash's process group/cgroup and are stopped with Dash. For newer manager-integrated streaming, firmware's manager owns camera teardown.

## Resource policy

Tune the named constants in [`policy.go`](../backend/policy.go): eight assumed cores, up to eight media threads, one heavy job, 96 MiB backend Go heap target, 24 MiB supervisor target, and a 32 MiB maximum individual FFmpeg allocation. There is **no CPU quota, CPU pinning or device topology detection**. These are concurrency and soft memory budgets, not a promise that a process can never OOM.

Systemd requests aggregate `MemoryHigh=256M`, `MemoryMax=384M`, zero swap, 128 tasks and 1024 open files. The installer reads memory values from the app binary's `--resource-settings`, so tuning the constants also updates the unit. Kernel support differs by AGNOS version; device mode deliberately does not probe controllers or claim every requested control is enforced. The Docker sandbox separately exercises enforced hard limits on the build host. `OOMPolicy=stop`, restart limits and process-group cleanup contain a Dash failure.

The published `IsOffroad=1` permits heavy work. Newer sunnypilot publishes this flag alone; older firmware also provides `IsOnroad`, which must be `0` when present. Missing or malformed `IsOffroad`, unreadable flags, and a legacy onroad veto keep work blocked. Parked observations are checked again before use so an interleaved flag update cannot silently pass as parked. A state watcher cancels it when the firmware leaves offroad or reports unknown state (100 ms polling). Only one bounded indexing batch, conversion, export or managed live session owns the shared heavy slot. Metadata parsing and media output have bounded buffers; previews are capped at 960×540. The optional `--compute-gate` uses Dash-owned test flags that can only restrict actual firmware state; it never overrides an onroad/unknown device or changes firmware params.

Audio checks use a bounded 16 MiB/10-minute analysis window because the microphone can start after video. Truly empty AAC declarations remain silent; late-starting samples retain their recorded offset in preview and MP4 exports.

| Firmware state                   | Background behavior                                                                            |
| -------------------------------- | ---------------------------------------------------------------------------------------------- |
| Confirmed offroad                | Full fixed eight-thread media budget, up to two index workers; one shared heavy task at a time |
| Onroad                           | Heavy workers canceled/rejected; inventory scans, export cleanup and OTA work stay paused      |
| Unknown or startup not validated | Heavy work blocked until fresh, valid firmware state arrives                                   |

In device mode, onroad/unknown state also reduces Go scheduling to one processor (`IdleGoProcs`); confirmed offroad restores the eight-core budget. Onroad idle work is limited to the lightweight state watcher and small housekeeping timers. The server can still answer explicit requests for cached metadata, existing recordings and diagnostics. Keeping the service reachable requires some CPU; it does not run background media encoding or indexing. The appearance setting named Performance mode controls browser effects and cannot override the firmware compute gate.

Dash reads the manager-published [IsOffroad state](https://github.com/sunnypilot/sunnypilot/blob/9bebe42f735e8859421ca3cfcb2f57377bb1f954/openpilot/system/manager/manager.py#L141-L143). The user preference `OffroadMode` is not itself used as evidence of state. Live camera capture additionally requires fresh, ignition-off `pandaStates` messages.

Parked indexing uses two bounded workers and splits the eight media decoder threads between them. One shared heavy-work slot covers each batch, then yields to playback/exports. Onroad or unknown state stops active indexing and prevents new heavy work; cached results remain available. Native HEVC durations use a streaming frame-boundary inventory, with FFprobe fallback for unfamiliar layouts. See the [device benchmark](benchmarks/indexing.md).

SQLite uses WAL and `synchronous=FULL`; each part commits separately. After restart, SQLite replays valid journals and the scanner skips committed, unchanged parts. A power cut can require redoing the in-flight batch (at most two parts), rather than the library. Thumbnails are synced and atomically renamed before their rows are published, and abandoned temporary images are removed by a completed scan. Confirmed database corruption preserves one private diagnostic copy under `cache/.index-corrupt` before rebuilding; transient I/O, locked databases or permission errors never discard the index. For cache backups, stop Dash first or use SQLite’s backup API; copying just `index.sqlite3` from a running service can omit committed WAL data. Never delete live `-wal`, `-shm` or hot journal files.

Indexing checks unchanged size/mtime fingerprints every 15 seconds and retains unchanged thumbnails without resetting idle progress. Failed telemetry parses are cached too; changing the source or explicitly rebuilding the index retries them. Playing, seeking and switching cameras never rebuild a ready index. Uncached HEVC previews only prepare their own media cache. Complete logs provide raw HEVC frame counts when their byte totals account for the file, avoiding a full video scan. The selected preparation queue takes priority over the library scan. Interrupted log tails retain complete records and mark their summaries as incomplete; indexing can recover a longer prefix from rlog when qlog is truncated. Zero-byte camera files are omitted from playback and MP4/video channels, while original-asset exports preserve them. MCAP records recovery notes and clearly identifies estimated timing where an interrupted log ends before video.

Exports have an 8 GiB input/output limit and free-space checks; incomplete outputs are deleted and completed exports expire after an hour. Preview input/output is bounded to 256 MiB per part and requires 2 GiB free storage. Rebuilding removes the disposable index, never source recordings.

## Recording readiness and dates

Unindexed sessions stay visible but cannot be selected or played. Each row shows its indexed-part count; **Prepare next** moves that session ahead without opening a viewer. Indexing pauses onroad or when state is unknown. Once every part has a cached timeline and at least one camera has playable footage, the row unlocks. Missing GPS/engagement metadata does not block video. A fully indexed session with no usable camera shows **No playable footage**. Dashboard links use the same readiness rule. Camera choices contain only indexed footage.

The player checks a separate readiness endpoint before loading HLS, rather than repeatedly fetching a manifest that returns 409. A restored session waits quietly if preparation is needed.

GPS UTC anchors the recording date. The first part can reuse an unsynchronized boot clock; Dash calibrates its start from a later GPS-timed part using the same monotonic clock. If that first monotonic timestamp is not indexed yet, an interim nominal-part estimate is labeled as estimated. Counter-ID sessions without a GPS date show **Date unavailable**, rather than treating a March boot/file timestamp as a proven capture date. These sessions remain viewable after their footage is indexed. Calendar graphs omit unknown dates.

## Continuous recording playback

Dash exposes a VOD HLS playlist for each recording/camera. The seek bar covers all available camera files; file boundaries are handled internally with HLS discontinuities. No whole-recording conversion or in-memory concatenation is required. Indexed durations reuse complete log frame counts where available, count native HEVC picture boundaries otherwise, and use ffprobe for containers or unfamiliar streams. Raw HEVC uses logger’s 20 fps timebase. Missing or unreadable camera files are omitted, so playback time covers available footage rather than filling gaps with invented frames.

The road preview streams original H.264 transport-stream files directly. High-resolution HEVC feeds are converted into cached H.264 transport-stream chunks on demand using the same parked-only, single-worker and bounded memory/disk policy as other previews. Existing cached chunks and original road footage remain readable onroad. Incomplete timeline indexing pauses playback until parked indexing finishes. Browser native HLS is used when available; otherwise HLS.js loads lazily with a 15-second forward-buffer setting, 15-second back buffer, and 32 MiB buffer target. This target is a client buffering heuristic, not a hard browser memory limit. See the [HLS.js API](https://github.com/video-dev/hls.js/blob/master/docs/API.md) and [ffprobe documentation](https://ffmpeg.org/ffprobe.html).

The player shows one camera at a time with a continuous seek bar, clickable timestamp entry (`seconds`, `m:ss`, or `h:mm:ss`) and playback speed control. Its Viewer options toggle cached road snapshots and recorded core engagement status. Select another camera in the player controls. On desktop, drag the sidebar boundary or the divider between video and map/metadata to resize panels; the browser remembers the split. Double-click to animate a reset, or press Enter for a keyboard reset; arrow keys and Home/End also resize. Mobile keeps Video / Map & data tabs. The map marks the playback position and follows it; panning/zooming or Full route pauses follow, and Follow resumes it. No GPS is inferred across missing intervals. Exports always include every available part of the session; there are no range selectors. Raw video downloads preserve the selected camera’s original parts in an uncompressed ZIP. MP4 joins that camera into one file; ZIP and MCAP include all original assets.

## Local HTTPS and PWA installation

HTTP serves the app and recordings, but browsers require a secure context for service workers on LAN IPs. Supply a certificate valid for your device hostname/IP and trusted by the phone/computer. A certificate warning bypass is not equivalent to a trusted installable PWA. Keep private keys out of this repository.

Put the certificate and key under `/data/dash/tls`, readable by `comma`, with the private key mode `0600`. Set `/data/dash/dash.env`:

```sh
DASH_TLS_ARGS="--cert /data/dash/tls/cert.pem --key /data/dash/tls/key.pem"
```

Restart the service, then open `https://DEVICE_IP:8088`. Trust your local certificate authority on each client through the normal OS certificate workflow. Dash precaches its interface only; it does not silently copy recordings to browser storage. MapLibre loads OpenStreetMap vector backgrounds automatically without a token; all rendering is on the viewing browser. Tiles require internet access and disclose the viewed area to the tile service. The recorded GPS overlay works when tiles are unavailable. Configure the Shortbread tile service by editing the served `map-config.json`; see [map configuration](maps.md). Dash does not prefetch map tiles or include them in the offline PWA cache.

Dash intentionally has no authentication. Anyone with network access to port 8088 can access recordings and request parked work. Keep it on your trusted hotspot/LAN and do not forward this port publicly. Requests with a foreign browser Origin are refused, but this is not a substitute for network isolation.

## Firmware live view

Dash supports a firmware-managed WebRTC path when the installed manager provides
`IsLiveStreaming`, and a Dash-owned adapter for older sunnypilot builds with the
installed camera and WebRTC libraries. The Go backend embeds the small Python
adapter and runs it only while a browser is watching. It starts only its own
camera process, refuses an existing camera owner, and never edits firmware or
writes driving params. Local signaling and ICE need no cloud server or STUN.

The local adapter streams one camera at up to 640×360 and 10 fps. Its encoder
uses the fixed media-thread budget and shares the single heavy-job slot with
indexing/exports. These constants live in `policy.go`. Native camera internals
remain firmware-owned code; Dash adds no CPU affinity or real-time priority.

The browser renews an opaque viewer lease every three seconds. Only that viewer
can renew or stop its session. Navigation, page hiding, or an explicit stop
closes it immediately when reachable; a lost browser expires after 12 seconds.
Onroad/unknown params stop work independently. The local adapter also subscribes
to fresh `pandaStates`, exits on ignition or missing/invalid ignition data, and
uses parent-death signals and process-group cleanup to prevent orphan capture.
ICE advertises only the local address that accepted the browser request, without enumerating interfaces. No vehicle-control channels are exposed. Live audio capture is not enabled.
Browser recording saves the incoming video locally.

## Power cycles and interrupted work

A persistent installation starts `dash.service` automatically when `/data` is mounted after a normal power cycle. Runtime installations (`--runtime`) do not survive reboot. Before `comma.service` starts, a small boot guard snapshots the persisted offroad flag. Dash waits for the firmware to replace it with a coherent new driving state, preventing the previous boot’s parked flags from permitting early work. Systemd owns the guard and its ephemeral `/run/dash` markers. Same-boot restarts retain validation; explicit installation after the firmware is active uses the ordinary state gate. Indexing, conversion and live capture still require confirmed parked state; unknown state keeps heavy workers stopped.

Completed recording parts are committed individually to SQLite WAL with full synchronization. An interrupted scan reuses those entries and retries unfinished parts. Thumbnails are flushed and renamed atomically. This recovery is tested with a killed writer; it does not simulate a device storage controller losing power.

The SSH installer flushes staged release contents before switching `current`, and replaces the bootstrap and lifecycle files using flushed temporary files and atomic renames. It never removes a release still referenced by the boot pointer. Reinstalling an identical tag reuses its files; if that release is damaged, install another tag or a fresh `--source` build. A first installation must finish systemd registration before automatic startup is available. Device acceptance testing verified automatic systemd recovery after abruptly killing Dash’s supervisor, process-group cleanup and preservation of all 572 cached parts. A physical power cycle exposed an early-boot unit-loading failure in the former `/data` symlink registration. The installer now migrates it to a boot-readable unit copy; isolated tests verify readability with `/data` unavailable. A subsequent device reboot verified automatic startup and unchanged cached entries. Boot-guard tests hold startup on old or inconsistent flags and verify fresh-state and same-boot restart behavior.

## AGNOS updates and removal

AGNOS documents `/data` as persisting across OS updates, unlike the other system partitions. Keep the bootstrap, versioned releases, update configuration/journal, PWA, cache, certificates, and unit source there. `/etc/systemd/system/dash.service` and its enablement symlink may be lost during an update. The bundled media tools remain with Dash under `/data`. This persistence distinction is documented in the [AGNOS 8 release notes](https://blog.comma.ai/094release/).

After an AGNOS update, while parked, restore Dash’s systemd unit copy and enablement, then verify startup:

```sh
sudo bash /data/dash/install.sh
sudo bash /data/dash/verify-service.sh
```

The script restores the copied unit and enablement link and checks fresh supervised startup again. Dash does not patch sunnypilot's manager or install an extra boot hook to resurrect itself. A firmware-only update normally leaves `/data/dash` alone, but can change log schemas or streaming APIs. A factory reset/reflash or explicit data wipe can remove `/data`; keep exported recordings elsewhere.

To remove Dash, its cached exports, static tools and both persistent/temporary systemd registrations:

```sh
make device-uninstall DEVICE=comma@DEVICE_IP [DEVICE_KEY=/path/to/key]
# Or, directly on the parked device:
sudo bash /data/dash/uninstall.sh
```

The script validates both registration locations before stopping Dash, cleans up orphan enablement even when no unit is loaded, refuses a foreign service or symlinked install directory, removes `/data/dash`, and leaves recordings, firmware and system packages untouched.

## Device acceptance check

Verify startup with the helper, then review actual recordings, thumbnails, a complete playback timeline and MP4/ZIP/MCAP exports. Test onroad/unknown lockout using Dash-owned `--compute-gate` params; do not alter firmware driving params. A LAN browser with iPhone viewport/touch emulation validates responsive behavior, but does not replace an actual iPhone/Safari media-decoder test.
