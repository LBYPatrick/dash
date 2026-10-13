# Device sandbox

The Docker sandbox runs the actual Go backend, FFmpeg, SQLite index, and built Vue app against synthetic recordings. It lets you review behavior without SSHing into the device in your car. Docker is a development dependency; native device installation continues to use systemd.

## Start and test

Install Docker Desktop or a compatible Linux Docker Engine with Compose 2.17+ and cgroup v2, plus Node on your development computer. If Go is installed, the helper seeds only this project’s checksum-verified module archives from the local cache to avoid redundant downloads. With no local Go toolchain, the container downloads them normally. Start the engine, then:

```sh
make sandbox-up
```

Open <http://127.0.0.1:8089>. The first build downloads dependencies, runs frontend tests and Go race tests, and generates a small synthetic footage library. The container starts with **unknown** driving state every time, including after a restart. Use the amber **Device sandbox** control to select Parked before indexing or export. Select Onroad or Unknown to check that new work is refused and active work is canceled.

```sh
make sandbox-test      # Actual container API, exports, and resource checks
make sandbox-smoke     # Chrome browser integration against the container
make sandbox-logs      # Follow recent container logs
make sandbox-down      # Stop the sandbox; preserve cache volumes
```

The test commands use Node on your development computer. `sandbox-smoke` also uses local Chrome (override `CHROME_PATH` if needed) and covers the actual interface, multi-camera playback, diagnostic download, mobile layouts, and offline shell. It exercises state lockout, rebuilding/indexing, thumbnails, continuous HLS playback, ZIP/MP4/MCAP exports, diagnostics, and actual Docker resource configuration. It also checks that unbounded container startup fails and that restarting returns to Unknown while retaining diagnostic logs. It leaves the sandbox in Unknown state. Backend tests during the image build additionally interrupt a running FFmpeg process on an ignition transition.

Change the host port with `DASH_SANDBOX_PORT=8090 make sandbox-up` and use the same variable for `sandbox-test`. To discard the sandbox's disposable cache and params volumes, run `docker compose down --volumes`. No host recordings, SSH keys, firmware directories, or driving params are mounted. The image contains only the generated test library. The simulator is bound to localhost; its state-changing API is enabled only by `--simulation`, which device units never use.

## Isolation and resource checks

The runtime uses an unprivileged user, a read-only root filesystem, dropped Linux capabilities, no privileged mode, no Docker socket mount, a 32 MiB temporary filesystem, and a five-second shutdown deadline. Cache and params are isolated named volumes. Compose sets 0.5 CPU, 384 MiB memory, no swap, and 64 tasks, and bounds container logs. See Docker's [Compose service controls](https://docs.docker.com/reference/compose-file/services/) and [resource constraint documentation](https://docs.docker.com/engine/containers/resource_constraints/).

The backend verifies the **actual** container cgroup files before starting workers. Missing or excessive CPU/memory/swap/PID limits fail startup. Docker does not apply systemd's `MemoryHigh=256M`; the sandbox checks the hard memory limit and retains Dash's Go/FFmpeg/RAM monitoring. `make sandbox-test` also checks Docker's inspection result, preventing a Compose-only declaration from being mistaken for an enforced policy.

The sandbox runs Linux userspace on the host architecture (ARM64 on Apple Silicon), with Docker Desktop providing its Linux VM. It does not emulate a comma 4 camera, AGNOS kernel, firmware scheduler, thermal behavior, or sunnypilot's camera manager. Firmware live view correctly reports unsupported here. Browser simulation does not validate actual systemd boot/update persistence or the SSH installer against a physical device. Use the read-only `make device-check DEVICE=comma@DEVICE_IP` when ready to inspect your device's requirements; no installation is performed by that command.

## Debug reports

Open the device card in Dash and choose **Download backend diagnostics**. The text report contains current runtime/driving/index state, resource controls, and up to roughly 1 MiB of recent application logs. Logging persists in three files of at most 2 MiB each, across backend restarts, while systemd's journal or Docker logs retain lifecycle output separately.

Downloading works while onroad or unknown and does not start an indexer, encoder, export, or compressed archive job. It excludes recordings, GPS samples, telemetry payloads, environment variables, and request bodies. Error messages can include recording names and paths; review the report before sharing. There is no automatic upload. If the backend cannot start at all, download is unavailable: use `journalctl -u dash.service` on a device or `make sandbox-logs` in Docker to capture the failure.
