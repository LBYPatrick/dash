# Changelog

All notable changes to Dash are documented here using Keep a Changelog.

## [Unreleased]

### Changed

- Restyle Dash with glass controls, floating viewer chrome, optical edge highlights, system typography, sliding selections and reversible sheet/menu transitions, including accessibility fallbacks.

- Simplify recorded playback to one selected camera at a time and match Connect’s draggable, persistent video/map divider with keyboard controls and reset.

- Replace segment-based playback with continuous HLS recording timelines and one shared timeline for multiple feeds.
- Default exports to the full recording and index video durations with parked-only ffprobe workers.

- Match Connect’s Mapbox Light v11 basemap, route outline and map controls, with automatic loading and a route-only network fallback.
- Move Overview, Recordings, Live and device switching into the desktop top bar and a mobile side drawer.

### Added

- Add a session viewer that fits the viewport, with desktop video/map panels, mobile tabs, and a three-dot actions menu.

- Add the Vue PWA and Go dash-backend for local footage browsing, thumbnails, incremental indexing, multiple feeds, metadata, maps, and overview statistics.
- Add backend ZIP, MP4, and MCAP exports using Dash schemas and original-file attachments.
- Add a managed, parked-only firmware live-view adapter and browser recording.
- Add systemd startup, bounded restarts, process cleanup, enforced resource checks, and SSH installation with read-only preflight and activation rollback.
- Add an isolated Docker sandbox with synthetic footage, driving-state controls, container resource verification, and integration tests.
- Add bounded persistent backend logs and a frontend diagnostic download without footage or GPS samples.
- Add Makefile workflows, automated tests, and installation, sandbox, schema, and AGNOS persistence documentation.
