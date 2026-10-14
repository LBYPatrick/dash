# 003 — Bridge occasional asynchronous state changes

- **Status**: DONE
- **Commit**: 1fde25d
- **Severity**: LOW
- **Category**: Missed opportunities, state indication
- **Estimated scope**: LiveView.vue, ExportDialog.vue, UpdatesPanel.vue, src/shelf.css

## Problem

LiveView.vue:234 removes its connecting placeholder instantly on connection. ExportDialog.vue:188–213 replaces the preparing button with Download again instantly. UpdatesPanel.vue:191–201 inserts/removes download progress abruptly. These are occasional task transitions, not rapidly repeated navigation/data.

## Target

Live placeholder: opacity only, 160ms cubic-bezier(0.23,1,0.32,1), preserve overlay position (never fade video). Export completion/recovery action: opacity only 140ms same curve, never delay automatic file download. Update progress: opacity only 140ms same curve, never animate numeric progress/width. Use Vue Transition (interruptible CSS) and existing semantic status/live region; avoid simultaneous overlapping interactive actions. Reduced motion: opacity only 120ms; performance and keyboard: immediate. Reuse shared classes/tokens in src/shelf.css.

## Repo conventions to follow

Vue Transition already used in App.vue for toast/index activity. Performance is :root[data-performance='true']; keyboard is :root[data-interaction='keyboard']. Existing global reduced-motion disables all transitions, so scoped fade exception needs explicit specificity and !important only for opacity transitions. No movement/blur/continuous loops.

## Steps

1. Wrap only placeholder, completion action and update progress as independent Transition surfaces with exact CSS values. Give completion action a stable keyed wrapper if needed; don't fade or defer the native download call.
2. Define shared feedback fade rules in Shelf CSS, including proper reduced/performance/keyboard ordering.
3. Preserve all network, parked guards, cancellation and recording logic; root will separately fix clip retention.

## Boundaries

Do not edit App.vue, RecordingPlayer.vue, global layout or vendor Sheet. No chart, metric, drive-list, route or clock animation. No new dependencies.

## Verification

npm run build; updates-smoke; export UI mocked completion verify automatic download fires immediately. Inspect transition computed properties (opacity only), cancel/reopen quickly, reduced/performance/keyboard behavior. Feel-check status at 20% playback, ensure no double-exposed clickable buttons or invisible hit targets. Physical mobile live stream remains unverified.

## Execution result

Implemented in `db71a05`; verified with production typecheck/build and actual-browser `make motion-smoke`. Native keyboard/reduced/performance behavior, rapid reversal, zero measured divider discontinuity and immediate mocked export handoff passed. Physical Safari remains unverified.
