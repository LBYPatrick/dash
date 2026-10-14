# 002 — Make divider reset interruptible

- **Status**: DONE
- **Commit**: 1fde25d
- **Severity**: MEDIUM
- **Category**: Interruptibility, accessibility
- **Estimated scope**: src/components/ResizablePanels.vue, src/shelf.css

## Problem

ResizablePanels.vue:32 seeds dragging from `width.value`, which is the reset destination while grid-template-columns is interpolating. Capturing midway jumps to that destination. SidebarResize.vue:24 already reads the rendered width before disabling reset. Shelf.css:1078 disables resetting grid transitions for keyboard/performance but omits the sidebar handle's `left` transition defined at :888.

## Target

Keep the explicitly requested double-click reset at existing `--t-panel:260ms`, `--ease-sheet:cubic-bezier(0.32,0.72,0,1)` (a documented layout-transition exception). Grab during reset at the current rendered divider position, then follow pointer immediately. Keyboard/performance/reduced motion reset both grid and handle instantly. Purpose feedback/spatial consistency; occasional reset, frequent dragging must not ease.

## Repo conventions to follow

Follow SidebarResize.vue's DOM measurement before resetting=false and pointer-capture/multitouch guards. No new dependencies/tokens.

## Steps

1. In ResizablePanels.vue start(), measure actual `.session-video` rendered width and root width; clamp to supported range, apply before clearing reset; seed starting drag width from measured percentage.
2. Include sidebar-resize-handle under resetting workspace in keyboard/performance overrides in src/shelf.css.
3. Keep pointer-cancel/unmount cleanup intact.

## Boundaries

No drag spring/easing, no mobile layout edits, no vendor code changes. Do not report purposeful layout reset as a performance defect.

## Verification

npm run build. Start reset at wide split; grab mid-reset without moving pointer: divider must not jump more than ~2px. Move/release to verify capture. Keyboard Enter and performance reset must show no grid/handle animation. Reduced motion stays immediate. Slow-motion inspection at 20% confirms no jump.

## Execution result

Implemented in `db71a05`; verified with production typecheck/build and actual-browser `make motion-smoke`. Native keyboard/reduced/performance behavior, rapid reversal, zero measured divider discontinuity and immediate mocked export handoff passed. Physical Safari remains unverified.
