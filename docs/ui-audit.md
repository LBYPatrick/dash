# Shelf interface audit

Dash uses Shelf's neutral palette, tonal fills, compact typography, control radii, blue accent, icon grid and motion vocabulary. The browser uses solid panel surfaces in place of Shelf's operating-system window material. Sheets and menus are opaque in both themes. Adapted source is credited in [LICENSES/Shelf.txt](../LICENSES/Shelf.txt). Shelf’s sheet widget and dismissal stack are vendored directly with a small Dash API adapter; see [source provenance](../src/vendor/shelf/README.md). Its Electron and translation runtime are not needed. Its icons were adapted directly, removing the Lucide runtime dependency.

## Component review

| Component                        | Before                                          | After                                                                                                                                      | Reason                                                   |
| -------------------------------- | ----------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------ | -------------------------------------------------------- |
| Desktop bar and navigation       | Separate glass controls and a tall bar          | Compact Apple-style toolbar: device context left, centered segmented destinations, utilities right; distinct light-mode chrome             | Matches Shelf's compact application chrome               |
| Mobile navigation                | Drawer with unrelated material                  | Menu left, current view centered, settings right; destinations/device/help in the drawer with focus return                                 | Preserves navigation structure across screen sizes       |
| Recording library                | Glass search/filter/card surfaces               | Flat sidebar, tonal controls, quiet selected rows, readable secondary labels                                                               | Makes footage the visual priority                        |
| Recording viewer                 | Decorative preview label and native camera menu | Millisecond timer/seeking, smooth media-clock updates, speed control and optional cached snapshots/recorded engagement                     | Keeps viewing focused; switching cameras retains time    |
| Viewer splitter                  | Thin gesture affordance                         | Keyboard-accessible sidebar and viewer separators; double-click animates reset                                                             | Keeps the requested side-by-side workflow                |
| Metadata and overview            | Different card treatments                       | Square right pane with shared recessed fills and restrained card radii                                                                     | Gives statistics the same hierarchy as the library       |
| Map                              | Separate toolbar material                       | Compact opaque Shelf toolbar; MapLibre/OSM vector basemap with local light/dark styling; route colors retained, playback marker and Follow | Matches UI while preserving requested map appearance     |
| Session actions                  | Inline details popup susceptible to clipping    | Native top-layer popover, viewport placement, keyboard navigation                                                                          | All actions remain reachable in short windows            |
| Settings                         | Fixed popup and repeated close/header markup    | Shared content-sized Sheet; immediate theme/performance controls                                                                           | One predictable popup behavior                           |
| Device/help/original-file sheets | Independent dialog layouts                      | Shared Sheet header, sizing, bounded body scrolling and focus return                                                                       | Consistent dismissal and content growth                  |
| Export                           | Segment range selectors                         | Full session only; ZIP, MP4, raw camera archive, MCAP                                                                                      | No accidental partial exports or range questions         |
| Export/live/simulation selectors | Browser-native select menus                     | Shared opaque top-layer SelectMenu with keyboard/typeahead                                                                                 | Consistent styling without clipping inside modal dialogs |
| Live view and recording controls | Separate icon/field styling                     | Shared icon, button and selector vocabulary; state explanations retained                                                                   | Keeps unsupported or blocked states understandable       |
| Diagnostics/errors/empty states  | Mixed button and text treatments                | Shared buttons, muted text and accessible status/error messages                                                                            | Recovery remains close to the failed action              |
| Motion and accessibility         | Glass effects and independent transitions       | Shelf curves/timings; reduced motion and performance fallbacks; contrast-tested text                                                       | Predictable motion with cheap rendering paths            |

## Flow review and evidence

- Connecting and switching devices share the same sheet. Backend error messages stay in context; local same-origin serving and sample exploration remain available.
- Library search, sorting, bookmarks, overview and live navigation keep window geometry stable across tabs; only overflowing content shows Shelf’s thin transparent scrollbars. Window chrome spans the full viewport without a page-wide gutter or fixed-width pane caps. Theme changes preserve layout and update existing maps. Each renderer fits a route when its mobile panel first becomes visible, without resetting zoom on subsequent resizing.
- A session shows one camera and the map/metadata side by side. Mobile uses Video / Map & data panels. The session viewport never scrolls; long popup content scrolls inside its body with the close button visible.
- Viewer controls seek across the entire session. Snapshot previews use cached road thumbnails without starting extra decoding. Engagement is shown only when recorded core selfdrive telemetry exists; missing samples display unavailable. Map Follow tracks the playback GPS point, pauses on manual exploration and resumes explicitly. Both panel boundaries reset with a short animation that respects reduced-motion and performance preferences.
- Actions return focus to their trigger before opening a sheet. Escape first closes a nested selector, then the sheet; dismissal restores opener focus. Shelf’s sheet supplies modal focus trapping and return; its shared dismissal stack handles nested overlays.
- Sheets grow around their centre when options or status change, and cap their height to the viewport. Menus position above/below their anchor and use the browser top layer for anchored menus. Sheets use Shelf’s portal and transition lifecycle.
- Exports discover all available session parts on the backend, sort numerically and ignore supplied subsets. Raw video is an uncompressed ZIP of the selected camera's unchanged original parts, preserving boundaries and filenames. ZIP and MCAP contain every available asset; MP4 joins the selected camera. A missing camera part fails explicitly rather than quietly delivering a partial session. The 8 GiB limit fails the full-session export instead of suggesting a range.
- Parked state is still required to prepare exports or stream live video. Diagnostics downloads remain available independently of heavy processing. Existing backend race/cancellation tests verify processing gates; UI restyling does not change them.

`make viewer-smoke` checks persisted sidebar resizing/reset, timestamp validation, speed, full-session seeking, map Follow, optional snapshot/engagement controls and desktop/mobile/landscape accessibility without viewport scrolling. `make shelf-smoke` checks WCAG A/AA rules on recordings, overview, live, actions, sheets, selectors and mobile layouts, plus Escape/focus behavior, repeated menu selection, centred sheet growth, symmetric popup padding without phantom gutters, and landscape overflow. `make appearance-smoke` checks contrast in light/dark and performance modes, theme persistence/System/cross-tab updates and stable gutters. `make design-smoke` checks five viewports, drawer focus and accessibility media preferences. `make sandbox-smoke` verifies real fixture playback, camera switching, full-session exports, diagnostics, onroad lockout and offline behavior in Docker.

These checks use synthetic recordings. Physical-device live streaming and recording still require a supported sunnypilot installation; no comma device was installed or accessed during this audit.

## Toolbar and timer refinement

| Before                                                         | After                                                                              | Why                                                                 |
| -------------------------------------------------------------- | ---------------------------------------------------------------------------------- | ------------------------------------------------------------------- |
| Logo/caption, destinations and device/utility controls compete | Three stable desktop regions: device, centered destinations, utilities             | Clear grouping and predictable wayfinding                           |
| Mobile logo and multiple utility buttons crowd the bar         | Leading navigation, centered current view, trailing settings; help in the drawer   | More room for readable context and touch targets                    |
| Repeated local-recordings labels                               | One device identity beside a quieter Dash mark                                     | Removes redundant chrome                                            |
| Whole-second timer and coarse playback updates                 | `m:ss.SSS` timer, 0.001-second slider steps and display-synced media-clock updates | Precise seeking and smooth progress without easing behind the video |

Playback motion is functional state indication: it reads the media clock with `requestAnimationFrame`, without extrapolation or easing. Pausing, buffering, hidden tabs and unmount stop the loop. Reduced motion and performance mode retain ordinary media-event updates; engagement lookup remains at the media event cadence; map interpolation uses the existing clock capped at 30 Hz. Seek precision describes requested timestamps, not extra frames beyond the source/browser. Desktop navigation reuses the existing shared selection highlight; keyboard navigation stays immediate. No new animation dependency or background backend work was added.

## Dashboard statistics

| Before                                        | After                                                                                         | Why                                                                         |
| --------------------------------------------- | --------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------- |
| Static totals and a list of covered drives    | Metric cards choose a D3 distance/time/count graph; every retained drive remains discoverable | Connect totals to a useful next action without hiding incomplete recordings |
| Browsing activity has no date scope           | All/7/30/90-day scopes and drive/day/month grouping                                           | Find a useful period before inspecting individual recordings                |
| Graph interaction can mix hover and selection | Hover/focus previews; click/Enter pins a drive-list filter; Escape/Clear restores the list    | Inspection never unexpectedly navigates or changes results                  |
| Dense charts need horizontal scrolling        | Responsive bounded bar windows with Earlier/Later paging and a stable scale across pages      | Keep readable, keyboard-accessible targets on a phone                       |
| Unknown distance appears as zero              | Accepted GPS-pair count in cached summaries; unavailable and partial values are explicit      | Separate missing metadata from a stationary recording                       |

`make statistics-smoke` covers totals, metric/unit/group/range changes, deliberate selection vs inspection, arrow-key/Enter/Escape navigation, table controls, returning from a recording with its selection restored, bounded charts and lists, and light/dark phone/desktop accessibility. A separate browser-only 90-drive history tests partial/missing data and paging; it never creates recordings on a device. The dashboard reads ordinary index summaries and does not request GPS/sample arrays, exports or decoding jobs. D3's scale module is bundled with the lazy dashboard chunk. The existing single parked worker, sample budgets and systemd resource limits remain in force.

## Library controls

| Before                                                  | After                                                     | Why                                              |
| ------------------------------------------------------- | --------------------------------------------------------- | ------------------------------------------------ |
| Large filled All/Saved selector                         | Single Saved toggle, off for all drives                   | Removes a redundant choice and a heavy container |
| Search, filter and sort compete as separate filled rows | Search first, then a quiet Saved/sort utility row         | Clear hierarchy with more space for recordings   |
| Count badge and detached sort label                     | Plain filtered/total count; sort shows its current choice | Reduces noise while preserving context           |

Search, saved filtering, empty-state recovery and sort keyboard behavior are verified by `make shelf-smoke`. Controls remain usable at the minimum sidebar width and keep larger touch targets on phones. Frequent filtering has immediate feedback without a positional animation; no new dependencies or backend work are needed.

## Mobile scroll regression

On mobile the workspace switches from grid to block layout. Its detail pane previously grew to its content height while the fixed workspace clipped it: in the reproduced case the pane was 1,287px tall inside a 792px workspace, leaving no inner overflow to scroll. The mobile detail pane now has `height: 100%`, so dashboard/live content scrolls within the available workspace; the session viewer retains its explicit `overflow: hidden`. Scrollable detail and library panes contain overscroll without blocking native touch panning.

`make mobile-scroll-smoke` sends Chromium native touch-scroll gestures, verifies bottom content is reachable in portrait/landscape and a resized viewport, scrolls a browser-only extended drive list, and checks that the viewer and outer document remain fixed after opening/closing a sheet. These are browser-level regression checks, not physical-device Safari certification.

## Desktop navigation and map follow

| Before                                                  | After                                                                   | Why                                                           |
| ------------------------------------------------------- | ----------------------------------------------------------------------- | ------------------------------------------------------------- |
| Desktop top bar above a separate library                | Device, destinations and utilities in the resizable sidebar             | Give the content the full available height                    |
| Library shown next to Overview and Live                 | Contextual drive browsing only under Recordings                         | Keep the navigation useful without competing with the page    |
| Index refresh reapplies full-route bounds               | Fit once per session; preserve GPS route identity across summary polls  | Keep background indexing from fighting Follow or exploration  |
| Follow jumps at native media event cadence              | Interpolated GPS positions from the existing media clock, at most 30 Hz | Keep movement synchronized without trailing easing animations |
| Mapbox marker sends GeoJSON to a worker on every update | Lightweight DOM marker; Leaflet uses incremental pans                   | Avoid unnecessary work during playback                        |

The frame clock stops while paused, seeking, buffering, hidden, or unmounted. Reduced motion and performance mode retain native `timeupdate`; hidden mobile map panels skip renderer updates and catch up when revealed. No backend work is added. Viewer smoke supplies a browser-only GPS track to verify intermediate marker movement, Follow centering/panning, stopped motion on pause, camera stability across an index refresh, and controlled visibility cleanup. The native and Docker previews now share the MapLibre renderer. Desktop checks cover the absent top bar, navigation/control fit, sidebar resizing, utility sheets, light/dark contrast, and stable pane geometry when switching views.

## MapLibre and OpenStreetMap vector maps

| Before                                            | After                                                                                                  | Why                                                                               |
| ------------------------------------------------- | ------------------------------------------------------------------------------------------------------ | --------------------------------------------------------------------------------- |
| Separate Mapbox and Leaflet renderers             | One MapLibre renderer using OSM Shortbread tiles                                                       | Same behavior in native and Docker builds, with no map credentials                |
| Filtered dark raster tiles                        | Local light/dark vector paint styles and system-font labels                                            | Readable theme-specific maps without proprietary style/font endpoints             |
| Map initialized behind hidden phone panels        | Visible-panel initialization; one worker and bounded tile cache                                        | Avoid offscreen work and unnecessary tile requests                                |
| Tile failure can obscure whether GPS is available | Keep the independent GPS overlay and show a precise background notice; SVG route preview without WebGL | Preserve recorded information when background services or GPU support are missing |

Theme changes update paint properties without replacing sources or the camera. OSM attribution stays expanded, including on narrow screens. Tests use an invented Shortbread tile rather than repeatedly requesting public tiles; a live viewport check separately confirmed OSM TileJSON and tiles load successfully, with locally rendered labels and no Mapbox calls.

| Before                                                                         | After                                                                                                                             | Why                                                           |
| ------------------------------------------------------------------------------ | --------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------- |
| Index maintenance and parked-processing explanations always occupy the sidebar | Rebuild in Settings; processing details in Help; active indexing enters/exits in 200 ms with reduced-motion/performance fallbacks | Keep browsing quiet while making ongoing work visible         |
| Prepare an export, then press Download                                         | Download recording starts preparation and automatically hands the completed file to the browser, with Download again as recovery  | One explicit action carries through to the file               |
| MCAP video available only as attachments                                       | All session cameras have Foxglove video channels alongside originals                                                              | Video can be inspected on the same MCAP timeline as telemetry |

| Before                                           | After                                                                                        | Why                                                   |
| ------------------------------------------------ | -------------------------------------------------------------------------------------------- | ----------------------------------------------------- |
| Camera selector floats above the video           | Camera selector joins bottom playback controls; narrow panels give it a dedicated second row | Keep video unobstructed and related controls together |
| Engagement toggle remains active without samples | Disabled “Engagement not recorded” action; persisted overlays stay hidden until data exists  | Explain availability without implying recorded state  |

| Before                                             | After                                                                                                 | Why                                                                        |
| -------------------------------------------------- | ----------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------- |
| Help and Settings occupy a separate sidebar footer | One compact icon group justified to the right of Dash; Shelf labels appear on hover or keyboard focus | Keep utilities together without permanent text or a second navigation area |
| Mobile Help lives in the navigation drawer         | Help lives inside Settings, returning to Settings when dismissed                                      | Keep mobile navigation focused on destinations and utilities together      |

## Delivery settings

Settings uses the shared Shelf popup and select menu for channel selection, automatic-check preference and check/download/restart progress. Downloads belong to the backend and survive popup dismissal; no change of channel implicitly downloads. Restart waits for backend health before reloading the PWA, including LAN HTTP without a service worker. Onroad/unknown state disables actions. Local source builds show their identity and an explanation instead of OTA controls. `make updates-smoke` exercises the managed flow, narrow layout and contrast/accessibility against a mocked update API; `make ota-smoke` exercises actual native activation/rollback and local-source startup under Linux cgroup limits.
