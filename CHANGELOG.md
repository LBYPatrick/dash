# Changelog

All notable changes to Dash are documented here using Keep a Changelog.

## [Unreleased]

### Added

- Detect attached USB drives automatically in exports and mount on copy, with a selectable relative save folder for every destination and live transfer speed, ETA and reused-byte accounting.

- Add durable whole-library original archives to USB (native ext4 and bundled static exFAT/NTFS), S3-compatible storage, Google Drive and OneDrive, with parked cancellation, checkpoint recovery, immutable protobuf manifests and safe eject.
- Add localized Shelf export/destination sheets, provider device-code authorization, private SSH/HTTPS configuration, and CD-injected OAuth app registrations from GitHub Secrets.
- Add a root-owned systemd USB helper with external-device validation, owned mount recovery, fixed memory budgets and bundled filesystem sources/licenses; keep web serving unprivileged.

- Stream whole-session ZIP, raw video, MP4 and MCAP downloads directly to the browser, with disconnect/ignition cancellation and fresh-job retries.

- Add a checkout-free one-line installer, with direct device or SSH target modes, main/nightly/dev selection, pinned releases, verified native bootstrap downloads, and shared parked-only systemd activation.

- Add a frontend polish acceptance check covering accent/theme contrast, nested modal isolation, keyboard focus, and precise timestamp controls across five languages and four phone sizes.

- Switch device Go scheduling between the full offroad budget and one processor onroad/unknown, keeping heavy work gated and reporting the active budget in diagnostics.

- Fit Live camera and bottom controls to phone and desktop viewports, with recording elapsed time and secondary details in a Shelf popup.

- Keep OTA in a dedicated popup under Settings; retain update health tracking after dismissal or tab reload, with explicit retry and stale-poll protection.

- Port Shelf's eight accent presets and OKLCH palette derivation with blue defaults, live Settings swatches and persistent cross-tab preferences. Reuse its borderless notification widget for playback errors, with Retry and Dismiss.

- Add recorded AAC playback with remembered mute controls and preserve session audio in MP4 and HEVC browser previews.
- Add a Dash-owned parked WebRTC adapter using the firmware's installed camera and Python media libraries, with early ignition checks, owned process groups and per-viewer heartbeat leases.

- Port Shelf's notification stack, swipe dismissal, close/actions and pause-aware expiry to the PWA with touch targets and reduced motion.
- Keep map Follow enabled during GPS gaps and show a small animated availability indicator until recorded fixes resume.

- Bundle pinned, static FFmpeg/ffprobe with matching sources, licenses and build recipes; deploy and update media tools with each release without device packages or Docker.
- Add clean SSH/device uninstall scripts, identity-key selection and persistent or runtime systemd registration. Restore AGNOS root to read-only after scoped boot-link setup/removal.
- Add recording readiness states and Prepare next: keep unindexed and empty sessions disabled across the library/dashboard, prioritize complete timelines and wait quietly before loading HLS.

- Add Shelf-compatible i18next/i18next-vue localization with five bundled languages, System following, persistent cross-tab preferences, translated controls/help/accessibility text, and locale-aware dashboard values. Accept decimal commas in precise timestamp entry.

- Shared mobile playback controls across video/map panels, compact Trip details, supported fullscreen and touch/keyboard-safe timestamp editing.
- Contextual browser navigation and retained Overview state, plus live clips that remain downloadable after changing views.

- Separate Linux/macOS build-host setup and source-to-device SSH installer, with explicit architecture selection, full local checks, verified minimal payloads, shared systemd activation and OTA disabled for local builds.

### Security

- Remove the copied local Mapbox token and all Mapbox build configuration; maps no longer require credentials.

- Remove the embedded browser map token from source and local Git history; use ignored `.env` configuration and credential-free OpenStreetMap defaults. Exclude env files from Docker build contexts.

### Changed

- Build and smoke-test static media on native ARM64 and AMD64 CI runners, with verified source-keyed caches and artifact handoff to release packaging.

- Move release, fixture, media, SDK and sandbox host tooling from standalone JavaScript/Python launchers to Go. Run browser acceptance as Vite+ framework tests; remove `.mjs` files and direct Node commands from Make, installers, Docker and CI.

- Use the full Vite+ toolchain for the frontend and shared JavaScript tooling: managed Node/pnpm, one frozen lockfile, Rolldown builds, Vitest, Oxlint and Oxfmt. Update host setup, Make commands, CI and the sandbox builder; retain Vue template type checks.

- Separate the Vue PWA into `frontend/` and Go service into `backend/`; keep shared npm workspace tooling, Make commands and release packaging at the root.
- Clarify persistent versus temporary systemd installation, boot verification, AGNOS recovery and clean removal in the installation guides.

### Removed

- Remove completed animation handoff plans and an unused screenshot generator; retain maintained design/validation documentation under `docs/`.

### Fixed

- Fail closed on damaged archive identity/inventory paths and keep unreachable upload cleanup from starving other destinations; validate short-phone export sheets in all five languages.

- Allow verified GitHub release-asset redirects when fetching pinned filesystem helper sources, and check archive pause/restart behavior through the synthetic HTTP API.

- Preserve the firmware recording cleanup reserve during cached exports, previews, native installation and OTA; bound short preview output separately from whole parts.
- Package recorded AAC correctly in streamed MP4 downloads and document both computer and direct SSH-session one-line installation.

- Include the root Dockerfile in the sandbox's Go test inputs so runtime-policy checks run inside the image.

- Keep short portrait playback controls within the viewport; fit localized timestamp entry and compact sandbox/landscape chrome while retaining touch targets and map space.
- Improve dark accent text contrast and inset keyboard focus, isolate nested sheets from background controls, restore mobile navigation focus, and describe recorded engagement confidence in text.
- Restore a selected mobile session after reload once the first index snapshot arrives, keeping unindexed footage unavailable.
- Preserve map Follow during Tab navigation, respect motion settings when zooming, and make toast stack exits and interrupted/held swipe gestures predictable.

- Refuse temporary installation over persistent boot registration, clean both registration layers during uninstall, and verify physical unit enablement alongside backend readiness.

- Use sunnypilot’s published `IsOffroad` state across backend workers, live capture, boot and installation, supporting firmware without the legacy `IsOnroad` key. Keep present legacy flags as a veto and recheck parked observations across parameter changes.

- Stop expired-export cleanup when offroad state ends between file and job cleanup.

- Start reliably before `/data` is mounted by registering a small systemd unit copy instead of a dangling early-boot symlink; migrate existing registration and remove only Dash’s own files. Wait for fresh firmware flags at boot so stale parked state cannot launch early work.

- Flush staged SSH installations and atomically replace the boot executable and lifecycle files; retain the selected release throughout installation and refuse conflicting same-tag contents.

- Resume indexing after abrupt shutdown with durable per-part WAL commits and atomic synced thumbnails; preserve recoverable journals and quarantine only confirmed corruption.

- Cache unchanged corrupt-log failures instead of retrying them every scan, keep idle index progress stable during inventory checks, and guard camera switches and HEVC preparation against starting indexing.

- Probe a full bounded recording part for late-starting AAC, preserve it in MP4 and HEVC previews, and invalidate earlier false-negative audio caches.

- Match interrupted streams to decoded packet positions so corrupt interior frames leave timestamp gaps and do not break MCAP conversion or shift surviving video frames.

- Ignore Chromium's stale audio decode counters after HLS camera/session changes so silent footage keeps its audio control disabled.

- Make every MCAP video message an independent H.264 keyframe with decoder headers so paused Foxglove seeks do not depend on preceding frames. Preserve recorded timing and original attachments.

- Preserve leading and internal microphone gaps when exporting sessions that only contain audio in some parts.
- Ignore declared-but-empty AAC tracks instead of failing video/MP4 muxing; cache codec checks and seek HEVC from indexed keyframes rather than repeatedly decoding full prefixes.
- Serve retained immutable frontend assets across release switches and prevent stale app-shell caching.
- Render disengaged intervals faintly and engaged intervals in the primary accent, reserving green/orange/red for explicit recorded confidence.

- Prepare HEVC playback in short cached chunks, avoid camera-triggered indexing and repeated full telemetry reloads, ignore canceled play requests during camera changes, and make playback errors dismissible. Show indexing activity only for changed recordings.

- Recover complete telemetry records from interrupted native logs, use longer rlog prefixes when available, and export typed MCAP/video with explicit recovery/timing notes and original attachments.
- Handle nonzero/gapped native camera indexes, truncated final video packets and zero-byte tails; retain unchanged thumbnails and avoid full raw-video scans when complete log byte counts account for frames.
- Derive session dates from GPS-calibrated monotonic clocks, including later parts after device time sync; label estimates and keep unknown dates out of calendar graphs instead of displaying stale March boot timestamps.
- Restore the previous release on SSH activation errors, canonicalize systemd unit ownership checks and allow clean reinstallation of root-owned startup configuration.

- Model sunnypilot recordings with native zstd cereal logs, 60-second rotation, full/reduced-rate telemetry and matching 20 fps camera indexes. Exclude repeated route initData from segment durations and date modern route IDs from recorded clocks.
- Export full-rate telemetry from rlog (qlog fallback), typed Protobuf messages with embedded descriptors, Foxglove GPS fixes, engagement, identity, segment boundaries and camera indexes. Use recorded camera EOF timestamps when present; reject misaligned indexes and disclose recovered interrupted prefixes. This replaces the JSON MCAP v1 contract with v2.

- Keep MCAP assets, telemetry and camera frames on the capture clock; preserve source modification times separately so copied files cannot inflate playback to days. Give synthetic session parts distinct UTC and monotonic clocks.

- Complete selector/action popover transitions, interruption-safe divider resets, immediate keyboard/performance handle resets, and subtle asynchronous task feedback.

- Export every camera as `foxglove.CompressedVideo` MCAP messages with decoder headers and frame timestamps, converting B-frames under parked-only worker limits; retain original attachments and independent Dash telemetry schemas.
- Start downloads automatically when export preparation finishes, retaining a retry link for browser download restrictions.

- Smooth map Follow with media-clock GPS interpolation and incremental pans; stop background index polls from resetting the camera; avoid per-update Mapbox GeoJSON worker messages and stop updates when paused, buffering or hidden.

- Restore mobile dashboard and detail-page scrolling by bounding content panes to the available viewport; retain the fixed session viewer.

### Changed

- Speed parked indexing with two bounded workers, cached-summary reads, container duration probes and streaming native HEVC frame counts; preserve immediate onroad/unknown cancellation and foreground access between batches.

- Centralize fixed comma 3X/4 budgets: eight cores/media threads, one heavy job, bounded Go/media allocations and requested systemd memory controls; remove device CPU quotas, affinity and system-resource probing. Keep host validation sequential with lower Go concurrency.

- Group icon-only Help and Settings beside the desktop Dash brand with Shelf hover labels; move mobile Help inside Settings with nested-sheet dismissal and focus restoration.

- Move camera selection into bottom playback controls, with a dedicated row in narrow viewers; disable engagement controls and hide persisted overlays when no engagement samples exist.

- Move index rebuilding into Settings and processing explanations into Help; show a gently animated indexing indicator only during active parked builds, respecting reduced motion and performance mode.

- Replace Mapbox/Leaflet with MapLibre GL JS and OSM Shortbread vector tiles, local light/dark styles and system fonts; initialize maps only when visible and preserve GPS when tiles or WebGL are unavailable.

- Remove the connected desktop top bar and consolidate navigation, device switching, drive browsing and utilities into the resizable sidebar; retain the mobile header/drawer.

- Simplify the drive-library controls into a quiet heading, primary search, and one compact Saved/sort row; show filtered counts without badges.

- Redesign the top bar around device context, centered desktop destinations and quiet utilities; simplify mobile chrome and keep help in the navigation drawer.
- Show millisecond playback timestamps and slider steps, with smooth media-clock progress that respects reduced motion and performance mode.

- Remove phantom reserved scrollbar strips from popups, menus and content panes; use Shelf’s thin transparent scrollbars only for overflowing content.

- Use a lightweight Leaflet renderer for credential-free OpenStreetMap, loading Mapbox only when configured; fit routes when mobile map panels first become visible.

- Remove fixed-width workspace/pane limits, load cached GPS on first open even when workers are paused, and preserve route layers across theme changes.

- Reuse Shelf’s popup widget and dismissal stack directly; remove the page-wide scrollbar strip and keep scrolling inside content panes.

- Fix map fallback reentrancy during theme changes and keep library recordings reachable in short landscape windows.

- Flatten the dark-mode library sidebar into a continuous surface, removing remaining inset highlights from search, filters and selected drives.

- Give the top bar one continuous background, reduce its vertical height, and flatten its navigation/device/utility controls.

- Remove decorative control/panel outlines and selected-tab borders; use fills and depth for separation, with focus indicators reserved for keyboard navigation.

- Reduce excessive corner rounding, correct text and control contrast in both themes, and reserve scrollbar gutters to prevent layout shifts between views.

- Restyle Dash with glass controls, floating viewer chrome, optical edge highlights, system typography, sliding selections and reversible sheet/menu transitions, including accessibility fallbacks.

- Simplify recorded playback to one selected camera at a time and match Connect’s draggable, persistent video/map divider with keyboard controls and reset.

- Replace segment-based playback with continuous HLS recording timelines and one shared timeline for multiple feeds.
- Default exports to the full recording and index video durations with parked-only ffprobe workers.

- Match Connect’s Mapbox Light v11 basemap, route outline and map controls, with automatic loading and a route-only network fallback.
- Move Overview, Recordings, Live and device switching into the desktop top bar and a mobile side drawer.

### Added

- Add `main`/`nightly`/`dev` delivery branches with channel-only CI, immutable GitHub build releases and per-channel OTA manifests; install verified prebuilt binary/PWA bundles over SSH, defaulting to latest main.
- Add Shelf-style managed updates in Settings with parked-only check/download/install, bounded SHA-256-verified extraction, systemd-supervised activation, process-group cleanup, durable startup rollback, channel selection and diagnostic reporting; exercise real Linux activation and failure recovery in Docker.

- Add dashboard totals and D3 activity graphs with period/group/unit controls, metric cards, deliberate bar-to-drive filtering, keyboard/table alternatives and bounded mobile history paging. Distinguish missing GPS from measured zero without fetching detailed telemetry for the dashboard.

- Add a persistent resizable recording sidebar with animated reset, balanced viewer padding, playback-follow maps, timestamp entry, speed control, and optional cached snapshot/recorded engagement rows.

- Adopt Shelf’s palette, icons, compact chrome, opaque menus and content-sized animated sheets across every view; add component/flow accessibility checks.
- Export every available session part for MP4, ZIP and MCAP, and add an unchanged raw-camera ZIP download; remove range selectors.

- Add saved Light / Dark / System appearance settings and an opaque performance mode that removes glass and blur, with matching route-map themes and contrast audits.

- Add a session viewer that fits the viewport, with desktop video/map panels, mobile tabs, and a three-dot actions menu.

- Add the Vue PWA and Go dash-backend for local footage browsing, thumbnails, incremental indexing, multiple feeds, metadata, maps, and overview statistics.
- Add backend ZIP, MP4, and MCAP exports using Dash schemas and original-file attachments.
- Add a managed, parked-only firmware live-view adapter and browser recording.
- Add systemd startup, bounded restarts, process cleanup, enforced resource checks, and SSH installation with read-only preflight and activation rollback.
- Add an isolated Docker sandbox with synthetic footage, driving-state controls, container resource verification, and integration tests.
- Add bounded persistent backend logs and a frontend diagnostic download without footage or GPS samples.
- Add Makefile workflows, automated tests, and installation, sandbox, schema, and AGNOS persistence documentation.
