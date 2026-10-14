# Mobile and motion audit

Audit baseline: `1fde25d`. Vue 3, CSS transitions/WAAPI, Shelf surfaces; no motion dependency. Frequency: navigation/filtering/seeking/reading charts are frequent, menus and task completion occasional, onboarding rare.

| Plan                                                   | Severity | Status | Dependencies           |
| ------------------------------------------------------ | -------- | ------ | ---------------------- |
| [001 Popover transitions](001-popover-motion.md)       | Medium   | TODO   | None                   |
| [002 Divider interruption](002-resize-interruption.md) | Medium   | TODO   | None                   |
| [003 Async state feedback](003-status-feedback.md)     | Low      | TODO   | Shared tokens from 001 |

Execute 001 → 002 → 003. Mobile flow implementation is separate under emil-design-eng. No new animation library is justified.

## Vetted animation findings

| #   | Severity | Category         | Location                     | Finding                                                      | Fix summary                              |
| --- | -------- | ---------------- | ---------------------------- | ------------------------------------------------------------ | ---------------------------------------- |
| 1   | Medium   | Physicality      | shelf.css:584–607            | Select opens with no transition despite declared rules       | Add origin-aware @starting-style         |
| 2   | Medium   | Interruptibility | glass.css:457, shelf.css:712 | Actions popover disappears immediately                       | Retain display/overlay during 140ms exit |
| 3   | Medium   | Interruptibility | ResizablePanels.vue:32       | Drag interrupts reset at target instead of rendered position | Measure before disabling reset           |
| 4   | Medium   | Accessibility    | shelf.css:1078               | Keyboard/performance grid snaps but handle slides            | Cover handle in overrides                |

## Opportunities (all gates applied)

| #   | Location             | Today                            | Purpose                   | Frequency  | Suggested motion                                                                                                       |
| --- | -------------------- | -------------------------------- | ------------------------- | ---------- | ---------------------------------------------------------------------------------------------------------------------- |
| 1   | shelf.css:604        | Menu entry snaps                 | Spatial consistency       | Occasional | Scale .96→1, opacity0→1, 180ms cubic-bezier(.23,1,.32,1); keyboard/reduced/performance immediate; helps locate surface |
| 2   | shelf.css:712        | Actions exit snaps               | Preventing jarring change | Occasional | Scale1→.96, opacity1→0, 140ms same curve and discrete display/overlay retention; immediate opt-outs                    |
| 3   | LiveView.vue:234     | Placeholder vanishes             | Preventing jarring change | Occasional | Opacity1→0 160ms same curve, reduced120ms opacity, keyboard/performance0ms; video stays still                          |
| 4   | ExportDialog.vue:188 | Completion action swaps abruptly | State indication          | Occasional | Opacity0→1 140ms same curve; reduced120ms, keyboard/performance0ms; never delay file handoff                           |
| 5   | UpdatesPanel.vue:191 | Progress region pops             | State indication          | Occasional | Opacity0↔1 140ms same curve; reduced120ms, keyboard/performance0ms; progress values/width immediate                    |

All pass speed (<300ms) and function gates: motions explain surfaces/task states without moving readable data. No hover motion added; existing hover effects require (hover:hover) and (pointer:fine).

## Deliberate rejections

- RecordingPlayer clock/seeking and TripMap Follow: functional synchronization; easing would cause lag (function gate).
- ActivityChart bars, totals and drive filtering: readable data and frequent changes must stay still (frequency/function).
- Workspace route entrances and keyboard focus/navigation: frequent/keyboard; immediate (frequency).
- Shelf intrinsic-height correction and requested splitter reset: documented settled exceptions; preserve existing sizing, not an opportunity for decorative changes.

Verdict: Dash already has most of the right motion. Repair incomplete transitions and add a few quiet task-state fades; no page stagger, bounce or animated graphs. Highest leverage is complete popover lifecycle. Handoff is `improve-animations execute plans/001-popover-motion.md`, then the remaining plans.

Executed in `db71a05` and browser-verified with `make motion-smoke`. The mobile flow audit and implementation record is [docs/mobile-motion-audit.md](../docs/mobile-motion-audit.md).
