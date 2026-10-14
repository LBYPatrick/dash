# Changelog

All notable changes to Dash are documented here using Keep a Changelog.

## [Unreleased]

### Added

- Shared mobile playback controls across video/map panels, compact Trip details, supported fullscreen and touch/keyboard-safe timestamp editing.
- Contextual browser navigation and retained Overview state, plus live clips that remain downloadable after changing views.

- Separate Linux/macOS build-host setup and source-to-device SSH installer, with architecture detection, full local checks, verified minimal payloads, shared systemd activation and OTA disabled for local builds.

### Security

- Remove the copied local Mapbox token and all Mapbox build configuration; maps no longer require credentials.

- Remove the embedded browser map token from source and local Git history; use ignored `.env` configuration and credential-free OpenStreetMap defaults. Exclude env files from Docker build contexts.

### Fixed

- Keep MCAP assets, telemetry and camera frames on the capture clock; preserve source modification times separately so copied files cannot inflate playback to days. Give synthetic session parts distinct UTC and monotonic clocks.

- Complete selector/action popover transitions, interruption-safe divider resets, immediate keyboard/performance handle resets, and subtle asynchronous task feedback.

- Export every camera as `foxglove.CompressedVideo` MCAP messages with decoder headers and frame timestamps, converting B-frames under parked-only worker limits; retain original attachments and independent Dash telemetry schemas.
- Start downloads automatically when export preparation finishes, retaining a retry link for browser download restrictions.

- Smooth map Follow with media-clock GPS interpolation and incremental pans; stop background index polls from resetting the camera; avoid per-update Mapbox GeoJSON worker messages and stop updates when paused, buffering or hidden.

- Restore mobile dashboard and detail-page scrolling by bounding content panes to the available viewport; retain the fixed session viewer.

### Changed

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
