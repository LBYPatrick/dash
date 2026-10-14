# Installation and operations

## Requirements

The target needs Linux ARM64 (typical device architecture; the installer checks `uname -m`), systemd, unified cgroup v2 exposing `cpu`, `memory`, and `pids`, system CA certificates, FFmpeg 5 or later (including `-fps_mode` support, libx264 and the `h264_metadata`/`hevc_metadata` bitstream filters) and ffprobe in the service PATH, a `comma` user/group, and read access to recordings and firmware params. Dash writes only its cache and the managed live-stream flag/ownership marker. It requires writable `/data/dash`, `/data/params`, and permission to install a unit into `/etc/systemd/system`.

Enable device SSH and configure your key using the [official device connection instructions](https://docs.comma.ai/how-to/connect-to-comma/). The device hotspot address is commonly `192.168.43.1`; use the address shown by your actual network. SSH and the HTTP interface must be reachable from the client. There is no internet or account requirement after installation.

Do not assume every AGNOS release has the required resource controllers. Check:

```sh
cat /sys/fs/cgroup/cgroup.controllers
systemctl --version
command -v ffmpeg
command -v ffprobe
ffmpeg -hide_banner -encoders | grep libx264
ffmpeg -hide_banner -bsfs | grep -E 'h264_metadata|hevc_metadata'
```

If cgroup v2 or a required controller is unavailable, the installer stops. It does not alter the kernel command line, reboot the device, or weaken resource controls. Such a device needs a compatible OS configuration before this Dash deployment can run. Install FFmpeg through the supported OS package workflow if absent; Dash does not automatically upgrade system packages or firmware. Use a parked device for installation and validation.

## SSH installation from your computer

```sh
make device-check DEVICE=comma@DEVICE_IP
make device-install DEVICE=comma@DEVICE_IP
```

The first command is read-only and verifies compatibility, parked state, FFmpeg/H.264/MCAP filters, sudo access, permissions, and 2 GiB free storage. The second downloads and verifies the latest successful `main` CI release, stages its prebuilt binary/PWA over SSH/SCP, rechecks parked state, installs the systemd service, and verifies actual kernel limits. The SSH client requires Python 3.9+ standard library; no build toolchain is needed for installation. Add `CHANNEL=nightly` or `CHANNEL=dev`, and optionally `RELEASE=TAG`, to select another channel or pinned build. See [delivery and OTA](delivery.md). A failed activation restores previous binary/PWA assets if available and leaves the service stopped. No SSH command is run automatically against your car during local sandbox testing.

For unpublished code or debugging on Linux/macOS, run `make setup-build-env` then `make device-install-source DEVICE=comma@DEVICE_IP`. That separate route builds and checks on the host, transfers only the finished binary/PWA, installs the same systemd service and disables OTA for the local build. `make source-release` packages without contacting a device. See [build-yourself installation](delivery.md#build-yourself-and-debug-installation).

## Files and startup

`/data/dash/dash-bootstrap` supervises `/data/dash/current/dash-backend`, which serves `/data/dash/current/dist`, reads `/data/media/0/realdata`, keeps disposable files in `/data/dash/cache`, and observes `/data/params/d`. It detects firmware capabilities in `/data/openpilot`. Override paths by editing a systemd drop-in, not firmware startup scripts. The unit source and installation helpers remain in `/data/dash`.

```sh
sudo systemctl status dash.service
sudo systemctl stop dash.service
sudo systemctl start dash.service
sudo systemctl restart dash.service
journalctl -u dash.service -f
sudo bash /data/dash/verify-service.sh
```

Boot uses `WantedBy=multi-user.target`; `/data` must be mounted first. There is no `nohup`, tmux daemon launcher, or Docker requirement. `Restart=on-failure`, a 10-second delay, and a limit of three starts per five minutes prevent tight restart loops. After fixing a persistent failure:

```sh
sudo systemctl reset-failed dash.service
sudo systemctl start dash.service
```

Use the frontend device card’s **Download backend diagnostics** action to collect bounded recent application logs and runtime/resource status. Reports exclude footage and GPS samples; review error paths before sharing. Logs persist under `/data/dash/cache/logs` with a maximum of three 2 MiB files. If the backend cannot start, use the journal instead.

Logs go to the journal; journal retention/persistence follows the device's journald configuration. `KillMode=control-group` stops the backend and all its child processes; after five seconds systemd kills survivors. `OOMPolicy=stop` stops the unit following an OOM event. `ExecStopPost` clears a Dash-owned live request even after backend failure. Firmware's manager is responsible for actually stopping its camera/encoder processes.

## Verified limits

The unit sets CPUQuota 50%, CPUWeight 10, Nice 15, MemoryHigh 256 MiB, MemoryMax 384 MiB, MemorySwapMax 0, TasksMax 64, and LimitNOFILE 1024. These are shared by Dash and subprocesses, not allocated separately per worker. The backend's `--require-systemd-limits` checks `/proc/self/cgroup` and the actual `cpu.max`, `memory.high`, `memory.max`, `memory.swap.max`, and `pids.max` files **before initializing workers**. Unlimited, missing, or excessive values cause startup failure. The installer also waits up to 55 seconds for a readiness marker published after ten seconds of backend health, verifies its bootstrap PID against systemd MainPID and its child PID against the service cgroup, and checks that its release matches `current`. It disables the service if startup health or kernel-limit verification fails.

The smaller Go memory target and sampled FFmpeg RSS limit are additional safeguards, not substitutes for kernel limits. Memory exhaustion can terminate Dash; it is not possible to guarantee that arbitrary input never causes a process OOM. The aggregate hard limit bounds Dash's memory consumption. Only one indexing segment, conversion, export, or Dash live session can own the compute slot.

Video durations, thumbnails, and metadata are indexed while parked; inventory scans recur every 15 seconds and reuse unchanged size/mtime fingerprints. Low RAM or a state transition cancels ongoing work. Export input/output is limited to 8 GiB with free-space checks; incomplete results are deleted, completed exports expire after an hour, and orphan cleanup runs while parked. Preview input and generated output are bounded to 256 MiB per segment and require 2 GiB free storage. Long or unusual files may need original-file downloads instead. Rebuild affects the index, never source recordings. Bookmarks do not protect recordings from firmware retention.

## Continuous recording playback

Dash exposes a VOD HLS playlist for each recording/camera. The seek bar covers all available camera files; file boundaries are handled internally with HLS discontinuities. No whole-recording conversion or in-memory concatenation is required. Indexed durations come from ffprobe; raw HEVC uses logger’s 20 fps timebase. Missing or unreadable camera files are omitted, so playback time covers available footage rather than filling gaps with invented frames.

The road preview streams original H.264 transport-stream files directly. High-resolution HEVC feeds are converted into cached H.264 transport-stream chunks on demand using the same parked-only, single-worker, CPU/memory/disk policy as other previews. Existing cached chunks and original road footage remain readable onroad. Incomplete timeline indexing pauses playback until parked indexing finishes. Browser native HLS is used when available; otherwise HLS.js loads lazily with a 15-second forward-buffer setting, 15-second back buffer, and 32 MiB buffer target. This target is a client buffering heuristic, not a hard browser memory limit. See the [HLS.js API](https://github.com/video-dev/hls.js/blob/master/docs/API.md) and [ffprobe documentation](https://ffmpeg.org/ffprobe.html).

The player shows one camera at a time with a continuous seek bar, clickable timestamp entry (`seconds`, `m:ss`, or `h:mm:ss`) and playback speed control. Its Viewer options toggle cached road snapshots and recorded core engagement status. Select another camera in the viewer toolbar. On desktop, drag the sidebar boundary or the divider between video and map/metadata to resize panels; the browser remembers the split. Double-click to animate a reset, or press Enter for a keyboard reset; arrow keys and Home/End also resize. Mobile keeps Video / Map & data tabs. The map marks the playback position and follows it; panning/zooming or Full route pauses follow, and Follow resumes it. No GPS is inferred across missing intervals. Exports always include every available part of the session; there are no range selectors. Raw video downloads preserve the selected camera’s original parts in an uncompressed ZIP. MP4 joins that camera into one file; ZIP and MCAP include all original assets.

## Local HTTPS and PWA installation

HTTP serves the app and recordings, but browsers require a secure context for service workers on LAN IPs. Supply a certificate valid for your device hostname/IP and trusted by the phone/computer. A certificate warning bypass is not equivalent to a trusted installable PWA. Keep private keys out of this repository.

Put the certificate and key under `/data/dash/tls`, readable by `comma`, with the private key mode `0600`. Set `/data/dash/dash.env`:

```sh
DASH_TLS_ARGS="--cert /data/dash/tls/cert.pem --key /data/dash/tls/key.pem"
```

Restart the service, then open `https://DEVICE_IP:8088`. Trust your local certificate authority on each client through the normal OS certificate workflow. Dash precaches its interface only; it does not silently copy recordings to browser storage. MapLibre loads OpenStreetMap vector backgrounds automatically without a token; all rendering is on the viewing browser. Tiles require internet access and disclose the viewed area to the tile service. The recorded GPS overlay works when tiles are unavailable. Configure the Shortbread tile service by editing the served `map-config.json`; see [map configuration](maps.md). Dash does not prefetch map tiles or include them in the offline PWA cache.

Dash intentionally has no authentication. Anyone with network access to port 8088 can access recordings and request parked work. Keep it on your trusted hotspot/LAN and do not forward this port publicly. Requests with a foreign browser Origin are refused, but this is not a substitute for network isolation.

## Firmware live view

Dash detects the managed `enabled` request and `IsLiveStreaming` manager integration in the installed firmware. It sets that flag and proxies receive-only camera signaling to localhost port 5001. See sunnypilot's [WebRTC service](https://github.com/sunnypilot/sunnypilot/blob/master/openpilot/system/webrtc/webrtcd.py) and [manager process configuration](https://github.com/sunnypilot/sunnypilot/blob/master/openpilot/system/manager/process_config.py). Detection is a compatibility check, not a guarantee across all branches.

Only road, wide-road, or driver camera selection is exposed. There are no vehicle-control data channels. The browser sends heartbeats; a missing heartbeat for 12 seconds, a driving/unknown state, or low memory clears the live request. Older unmanaged APIs are intentionally unsupported because Dash cannot reliably own their lifecycle. Firmware-owned streaming processes do not inherit Dash's cgroup budgets; validate firmware behavior on the target. Browser recording stays on the client and stops at approximately 128 MiB, with container support dependent on the browser.

## AGNOS updates and removal

AGNOS documents `/data` as persisting across OS updates, unlike the other system partitions. Keep the bootstrap, versioned releases, update configuration/journal, PWA, cache, certificates, and unit source there. `/etc/systemd/system/dash.service`, its enablement link, and OS-installed FFmpeg may be lost during an update. This persistence distinction is documented in the [AGNOS 8 release notes](https://blog.comma.ai/094release/).

After an AGNOS update, while parked, verify dependencies and run:

```sh
sudo bash /data/dash/install-service.sh
sudo bash /data/dash/verify-service.sh
```

The script reinstalls/enables the unit and checks actual limits again. Dash does not patch sunnypilot's manager or install an extra boot hook to resurrect itself. A firmware-only update normally leaves `/data/dash` alone, but can change log schemas or streaming APIs. A factory reset/reflash or explicit data wipe can remove `/data`; keep exported recordings elsewhere.

To uninstall the service while retaining cached data:

```sh
sudo systemctl disable --now dash.service
sudo rm /etc/systemd/system/dash.service
sudo systemctl daemon-reload
```

You can then remove `/data/dash` if its data is no longer needed. Original recordings are outside that directory.

## Device acceptance check

Before relying on a deployment, verify kernel limits using the helper, inspect journal logs, and perform an actual parked recording/export. Confirm workers stop when firmware changes to onroad and refuse work when either state parameter is unavailable. Confirm firmware streaming shuts down on disconnect, ignition, and service stop. Do not manually alter driving params on an operating vehicle to test Dash. Local automated tests use isolated synthetic params; hardware behavior and scheduler latency have not been validated here.
