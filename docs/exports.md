# Recording archives

Dash can copy **all local original recordings** to a USB drive, S3-compatible storage, Google Drive or OneDrive. Open **Export recordings** beside the drive library, or **Settings → Export destinations**. Choose a destination and **Copy all originals**. This includes every camera, recorded audio and original log file in each native session directory, including closed files whose metadata has not been indexed. Session MP4/MCAP/browser downloads remain separate actions in the viewer.

No original is deleted. Dash never formats, partitions, repairs or changes ownership of unrelated files on a destination. A conflicting destination file is preserved and reported. Finish with issues is different from a complete archive; review its results before deleting anything yourself.

## Parked operation and recovery

All inventory, hashing, copying, provider authorization and upload cleanup require confirmed firmware offroad state. Onroad or unknown state cancels active work through the same 100 ms watcher and shared heavy-operation budget used by indexing and conversions. The USB service also unmounts owned volumes. Starting the car does not schedule a background upload.

Progress lives separately from the disposable index under `/data/dash/transfers`. Rebuilding the index does not restart or erase an archive. SQLite uses WAL and full synchronization; upload sessions/chunks are checkpointed. USB partials are read back after a process restart; Google and Microsoft are queried for acknowledged ranges. A lost final response is recovered by inspecting the completed object.

The Continue after restart option resumes the same inventory once parked again. Turning it off leaves an interrupted batch paused for explicit Resume. A manual pause always remains paused. Closing the popup or browser does not cancel a requested batch. One unfinished batch is allowed at a time. Cancel retains completed files and cleans unfinished uploads while parked; unavailable destinations show pending cleanup and do not block a new batch. Configure an S3 lifecycle rule to expire abandoned multipart uploads as additional protection against a power cut before an upload ID could be checkpointed.

Locked, missing, changed and conflicting originals are recorded as issues. A later batch takes a new inventory and includes newly recorded files; unchanged verified objects are reused. USB uses SHA-256 read-back. Cloud verification uses file size and Dash SHA-256 metadata (S3/Google), or the provider's file hash/read-back (OneDrive). ETags are multipart receipts, not whole-file hashes. Do not edit archive objects or their integrity metadata outside Dash.

A corrupt transfer database is retained for repair; it does not disable playback or regenerate a new archive identity silently. Back up `transfers/` privately before repairs. It contains account tokens and upload capabilities and must not be included in shared diagnostic bundles.

## Archive layout

```text
dash-archive/v1/<persistent-source-uuid>/
  archive-schema.pb
  README.txt
  sessions/<native-session-id>/originals/<native-segment-directory>/<file>
  snapshots/<batch-id>/
    sessions/<native-session-id>/manifest.pb
    sessions/<native-session-id>/SHA256SUMS
    result.pb
    COMPLETE                      # or COMPLETE_WITH_ISSUES
  .partial/                       # USB only; unfinished copies
```

Original bytes stay unchanged. Names that are unsafe or ambiguous on Windows/case-insensitive filesystems are encoded as `~` plus hexadecimal UTF-8 bytes (long names use `~h` plus SHA-256); manifests preserve the original source path. Native lowercase recording names remain readable. Source IDs prevent devices from colliding in a shared bucket. Each batch has immutable manifests and a completion marker published **last**. Partial snapshots without a marker are incomplete.

Manifests use the typed [Dash archive protobuf schema](../backend/schemas/dash/archive/v1/archive.proto); its descriptor set travels with the archive. The per-session checksum entries use archive paths relative to the source UUID directory. Filesystem modification times are explicitly not treated as capture dates. Memory is bounded by streaming buffers and an inventory cap of 100,000 originals; exceeding it stops before copying.

## USB

Use the comma 4 auxiliary data USB-C port and a suitable USB-C drive or powered hub. Power delivery, host negotiation and compatibility still need testing with your actual drive. No USB drive was attached during initial device inspection, so hardware support is marked experimental in the UI.

| Filesystem | Implementation                | Conditions                                                                                       |
| ---------- | ----------------------------- | ------------------------------------------------------------------------------------------------ |
| exFAT      | Bundled static FUSE helper    | Recommended for interchange; large-file support; exclusive archive publication patch included    |
| ext4       | Existing kernel driver        | Features must be supported by the device's older kernel; newer incompatible features fail safely |
| NTFS       | Bundled static ntfs-3g helper | Dirty/hibernated volumes may be refused; no recovery or repair is attempted                      |

Select a discovered UUID; browser-supplied paths, commands and mount options are never accepted. Duplicate UUIDs are unavailable. Internal/system devices and symlink traversal are rejected. The helper validates USB sysfs ancestry and the opened block-device identity, probes the selected filesystem, and mounts only under `/data/dash/usb/<uuid>`. It creates and owns only a new `dash-archive` directory; an existing directory owned by another user is refused. No filesystem packages are installed on the device.

**Eject safely** pauses the batch, waits for its file handles to close, and requests a normal unmount. Wait for success before unplugging. If the drive remains busy, resolve that condition and retry; Dash never forces or lazily detaches a busy mount. Reconnect the same UUID to resume.

The root-owned `dash-usb.service` exposes only list/mount/eject over a private Unix socket. The web backend remains unprivileged. Both services have restart limits, journal logging and process-group cleanup. Fixed USB budgets are a 24 MiB Go heap target, systemd memory high 64 MiB/max 128 MiB, and 24 tasks; constants live in `backend/policy.go`. Kernel enforcement varies on AGNOS and is not discovered by probing resource controllers. FUSE workers run only for an explicitly mounted parked transfer and are stopped on ignition/unknown state.

Mount intents under `/run/dash-usb` allow owned mount recovery after service crashes. The root helper stays in the host mount namespace so its shared `/data/dash/usb` subtree reaches the sandboxed backend. It does not change global mount configuration. Uninstall refuses to remove Dash while a mount remains there.

## Google Drive and OneDrive

Official CD builds can carry Dash's OAuth **application registration**. Select Google Drive or OneDrive, name it and save; Dash opens the provider connection sheet. Approve the displayed short code on the provider's HTTPS page. Your password never reaches Dash. User access/refresh tokens are created on authorization and stored only in private backend configuration files, never in the PWA, repository, release manifest or diagnostic dump.

The repository owner supplies these **GitHub Actions secrets** to channel CD builds:

| Secret                      | Registration                                                                             |
| --------------------------- | ---------------------------------------------------------------------------------------- |
| `DASH_GOOGLE_CLIENT_ID`     | Google Cloud OAuth client for TVs and Limited Input devices; enable Drive API            |
| `DASH_GOOGLE_CLIENT_SECRET` | Matching device application's client secret                                              |
| `DASH_MICROSOFT_CLIENT_ID`  | Microsoft public client with device-code flow enabled; configure supported account types |

A Google client ID and secret must be supplied together. Missing registrations leave that provider available through private custom setup rather than pretending account connection is configured. Secrets are passed only to the release packaging step and embedded by the Go linker, without generated tracked source files. A distributed installed application's client secret **can be extracted from its binary**; it identifies the app and cannot protect a user's account. No user access token, refresh token, Firebase token, service-account private key or S3 credential belongs in CD.

Google requests `drive.file`, limiting access to files used/created by Dash. The default archive folder is created in My Drive. An arbitrary existing parent folder ID is not necessarily accessible with this limited scope; use a folder already authorized for this application. Microsoft requests `Files.ReadWrite` and `offline_access`, with the default archive in the user's drive. No Google Picker or broad whole-drive scope is required for the default flow. Provider consent policies, app registration eligibility, account restrictions and quotas still apply.

Sources: [Google device authorization](https://developers.google.com/identity/protocols/oauth2/limited-input-device), [Drive scopes](https://developers.google.com/workspace/drive/api/guides/api-specific-auth), [Microsoft device authorization](https://learn.microsoft.com/en-us/entra/identity-platform/v2-oauth2-device-code), [Graph resumable uploads](https://learn.microsoft.com/en-us/graph/api/driveitem-createuploadsession).

## S3 and private custom setup

Destination keys and account tokens stay in owner-only configuration files (`0700` directory, `0600` files). Enter credentials through SSH or trusted HTTPS, never plain LAN HTTP. HTTP account connection with a bundled application needs no credential form. Private custom setup also supports builds/forks with their own application registrations.

Inside the device's SSH Bash session:

```sh
/data/dash/current/dash-backend --configure-transfer -
# Paste one configuration object, then press Ctrl-D.
```

Alternatively pipe a private local configuration through SSH; shell history should contain the filename, not its contents:

```sh
ssh comma@DEVICE_IP /data/dash/current/dash-backend --configure-transfer - < /private/path/destination.json
```

S3 template (replace placeholders in a private, untracked file):

```json
{
  "id": "home-backup",
  "name": "Home archive",
  "kind": "s3",
  "endpoint": "https://s3.example.net",
  "region": "us-east-1",
  "bucket": "driving-archive",
  "prefix": "dash",
  "pathStyle": true,
  "secret": {
    "accessKey": "REPLACE_PRIVATELY",
    "secretKey": "REPLACE_PRIVATELY"
  }
}
```

Omit endpoint for AWS. Optional `sessionToken` belongs inside `secret`. Use bucket/prefix-scoped credentials permitting object reads, writes and multipart abort, plus bucket listing for missing-object detection. Conditional creation (`If-None-Match: *`) must be supported; providers that cannot honor it are refused rather than overwriting an existing object. The SDK signs requests with SigV4. Multipart parts are normally 10 MiB; buffers stream from files rather than retaining whole footage in RAM. No browser CORS bucket policy is needed.

Custom OAuth templates:

```json
{
  "id": "my-google",
  "name": "My Google Drive",
  "kind": "gdrive",
  "clientId": "YOUR_DEVICE_CLIENT_ID",
  "secret": { "clientSecret": "YOUR_DEVICE_CLIENT_SECRET" }
}
```

```json
{
  "id": "my-onedrive",
  "name": "My OneDrive",
  "kind": "onedrive",
  "clientId": "YOUR_PUBLIC_CLIENT_ID",
  "tenant": "common",
  "secret": {}
}
```

Then connect through Settings, or run `dash-backend --authorize-transfer ID` in SSH. OAuth device codes expire; restart connection if needed. Refresh tokens are renewed while parked. Reconnect when a provider revokes authorization. Disconnect removes Dash's local account tokens; revoke app access on the provider account page for provider-side revocation too.

For trusted HTTPS management, read `/data/dash/current/dash-backend --transfer-setup-key` privately over SSH, then enter it in the configuration sheet. It is an owner setup capability, not a Dashboard login. It stays in memory in that popup, clears on dismissal and is not returned by any API. Avoid pasting it into chat or bug reports. S3 HTTP endpoints are disabled unless a private profile explicitly sets `allowInsecureHttp: true`; reserve that for isolated synthetic storage.

## Testing without the car

The Docker sandbox has persistent synthetic transfer state and a synthetic USB volume. It uses only generated fixture recordings, not a host drive. Add its USB destination in Settings and export, interrupt/restart the container, then resume. This tests the durable engine and UX, not physical USB host negotiation or real filesystem drivers.

`make transfers-smoke` checks phone/desktop popup flows, progress/actions, OAuth connection and WCAG AA contrast against provider fixtures. Go race tests exercise S3 using the real AWS SDK, provider range recovery, token refresh/redaction, conflicts and parked cancellation. No real cloud account is used by these tests.

On a **Linux build host** with Docker, root mount namespaces, FUSE, loop devices and test-only `exfatprogs`, `ntfs-3g`, `e2fsprogs`, and `util-linux` installed:

```sh
make build-filesystems MEDIA_ARCH=arm64   # or amd64 for that host
make filesystem-smoke
```

The filesystem check uses newly created images inside an isolated mount namespace, including remount/resume and exclusive publication tests. It never targets physical disks. Channel CI builds native ARM64/AMD64 static helpers, verifies architecture/digests and runs these checks. Matching upstream sources, the exFAT patch, build recipe and GPL/LGPL notices ship in each release. Nothing installs test/build packages on the comma.

A physical drive copy/eject/power-cycle test and real Google/Microsoft/S3 account authorization remain separate acceptance steps. Do not describe protocol mocks or synthetic volumes as those hardware/account tests.

`make archive-smoke` also exercises a running synthetic backend's real HTTP API: all-original copying, pause/resume, onroad rejection, public-state redaction and index independence. Set `DASH_ARCHIVE_URL` for a local synthetic server. With the default Docker sandbox, `DASH_ARCHIVE_RESTART=1 make archive-smoke` additionally restarts only that container and checks durable manual pause. It refuses any non-synthetic backend.
