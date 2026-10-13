# Changelog

All notable changes to Dash are documented here using Keep a Changelog.

## [Unreleased]

### Changed

- Replace segment-based playback with continuous HLS recording timelines and one shared timeline for multiple feeds.
- Default exports to the full recording and index video durations with parked-only ffprobe workers.

- Load OpenStreetMap backgrounds automatically when a GPS route is displayed.

### Added

- Add a session viewer that fits the viewport, with desktop video/map panels, mobile tabs, and a three-dot actions menu.

- Add the Vue PWA and Go dash-backend for local footage browsing, thumbnails, incremental indexing, multiple feeds, metadata, maps, and overview statistics.
- Add backend ZIP, MP4, and MCAP exports using Dash schemas and original-file attachments.
- Add a managed, parked-only firmware live-view adapter and browser recording.
- Add systemd startup, bounded restarts, process cleanup, enforced resource checks, and SSH installation with read-only preflight and activation rollback.
- Add an isolated Docker sandbox with synthetic footage, driving-state controls, container resource verification, and integration tests.
- Add bounded persistent backend logs and a frontend diagnostic download without footage or GPS samples.
- Add Makefile workflows, automated tests, and installation, sandbox, schema, and AGNOS persistence documentation.
