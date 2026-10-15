# Mobile experience and motion audit

Audited from `1fde25d` using emil-design-eng, find-animation-opportunities and improve-animations. Completed changes and validation are recorded below. The product remains a crisp, local recording tool with Shelf surfaces. No animation dependency was needed.

## Mobile findings and implemented flows

| Before                                                                 | After                                                                                                                                      | Why                                                                                     |
| ---------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------ | --------------------------------------------------------------------------------------- |
| Map & data hides every playback control                                | One mounted video and one shared transport dock below either panel                                                                         | Play, pause, switch camera, change speed and seek while inspecting the recorded route   |
| Metadata cards consume most of a short landscape map                   | Compact Trip details action; full metadata in the shared Shelf popup                                                                       | Prioritize map space without losing vehicle, duration, distance or coverage information |
| Landscape hides the direct return action                               | Compact back button remains alongside session identity; heading and panel tabs share a row in landscape                                    | Return without opening navigation and recover usable content height                     |
| Opening a drive from Overview returns to the general library           | Context-aware return, same-document browser Back/Forward, cached Overview state and scroll                                                 | Preserve metric, table disclosure, selection, paging and browsing context               |
| Reload drops mobile users back at the library                          | Restore the active workspace/session from browser history after reconnecting                                                               | Avoid losing the session during ordinary reload; deleted sessions fall back to browsing |
| Play/skip/speed/timeline targets are only 24–38px high                 | Deliberate mobile transport rows and 44px coarse-pointer targets                                                                           | Reliable thumb operation without shrinking text or hiding core controls                 |
| Mobile fullscreen is hidden                                            | Capability-based standard fullscreen with Safari native-video fallback; controls follow the picture in standard fullscreen                 | Restore a useful footage-inspection feature rather than dropping it for phones          |
| Timestamp editing relies on Escape to abandon                          | Visible Cancel action, input errors associated with the input, Go keyboard hint                                                            | Complete the editing flow without a hardware keyboard                                   |
| Leaving Live destroys an unsaved recording                             | Finalized capture survives view recreation; peer/tracks still stop on leave/background                                                     | Avoid silent data loss while keeping live work tied to the active view                  |
| Narrow panel selection is only visual                                  | Video/Map buttons expose pressed state                                                                                                     | Make selected state available to assistive technology                                   |
| Safe-area and keyboard assumptions depend on desktop viewport behavior | Header top/landscape side insets, contained overscroll, 16px editing text, browser keyboard resizing and unzoomed visual-viewport fallback | Protect installed/notched layouts and keep zoom available                               |

The live capture remains one bounded, in-memory clip. New capture replaces it; reloading or closing the tab discards it. The UI explains this and retains Save recording for retries. It is not a persistent recording archive. Live support and resource/parked safeguards on the backend are unchanged. Metadata missing accepted GPS samples displays unavailable instead of claiming a measured zero.

## Motion decisions

| Before                                                           | After                                                           | Why                                                                                      |
| ---------------------------------------------------------------- | --------------------------------------------------------------- | ---------------------------------------------------------------------------------------- |
| Select popovers declare transitions but snap open                | Trigger-aware scale .96→1 and opacity entrance over 180ms       | Explain a surface appearing from its control                                             |
| Action popovers disappear at hidePopover                         | Retained 140ms opacity/scale exit with discrete display/overlay | Complete the same spatial story on dismissal                                             |
| Grabbing a divider during reset jumps to its destination         | Capture the rendered width before interrupting reset            | Keep the divider attached to the pointer                                                 |
| Keyboard/performance reset snaps the grid but slides the handle  | Grid and handle both remain immediate                           | Honor the same interaction preference everywhere                                         |
| Live placeholder, export completion and update progress teleport | Brief opacity-only task transitions                             | Clarify asynchronous changes without moving the video, numeric progress or readable data |

Feedback is 140–160ms using `cubic-bezier(0.23,1,0.32,1)`, 120ms opacity-only in reduced motion, and immediate in performance mode or keyboard interaction. Menu movement is removed in reduced/performance/keyboard modes. Existing Shelf intrinsic-height behavior and explicitly requested divider reset remain deliberate exceptions to compositor-only animation.

Rejected: animated charts/totals, drive-list stagger, route entrances, eased video clocks and eased map Follow. These are frequent or functional interactions; users need readable, synchronized state. Hover animations remain fine-pointer gated. The highest-leverage improvement is completing popover entry/exit, not adding decorative movement across the app.

## Verification and limits

- `make mobile-flow-smoke`: actual Chromium touch-layout flows at 320×568, 390×844, 667×375 and 844×390; shared playback/seeking on the map, trip details, contextual return, browser history, retained Overview state/scroll, active-session reload, standard fullscreen/return, 44px targets, fixed viewer geometry, useful map height and light/dark WCAG checks.
- `make live-capture-smoke`: browser-only signaling/recorder doubles; record, leave the view while capture is active, verify peer/track cleanup and background visibility shutdown, return and download the finalized clip after repeated navigation. No firmware or user camera is contacted.
- `make motion-smoke`: rendered menu entrance/exit timings and origins, rapid reversal, zero measured divider jump when reset is interrupted, immediate keyboard/performance resets, reduced-motion feedback and automatic export handoff before its recovery-link fade. Export APIs are mocked.
- Existing design, Shelf, mobile-scroll and appearance suites cover remaining surfaces, sheets, scrolling, contrast and performance mode. The enhanced viewer and statistics integration suites also pass with the new static site mounted in a limited Linux sandbox (50% of one core, 384 MiB, zero swap); the backend is unchanged. Formatting, typecheck, tests and production build pass.

Screenshots and logs live under ignored `.artifacts/`. Chromium emulation cannot certify Safari's native video fullscreen, physical keyboard/IME behavior, pinch/edge gestures, installed status bars, notches or touch feel. Those require a real iPhone/iPad/Android pass; no physical phone or comma device was accessed. The work adds no backend jobs and changes no resource or driving-state safeguards.
