# 001 — Restore complete popover transitions

- **Status**: DONE
- **Commit**: 1fde25d
- **Severity**: MEDIUM
- **Category**: Physicality, interruptibility, missed opportunities
- **Estimated scope**: src/shelf.css only

## Problem

SelectMenu's `.menulist` at src/shelf.css:584 defines opacity/transform transitions, but `.menulist:popover-open` at :604 lacks an entry starting style. Browser measurement found zero opening animations. SessionActions at src/shelf.css:712 inherits only opacity/transform from src/glass.css:457, so `hidePopover()` drops its exit immediately.

Current snippets:

```css
.menulist:popover-open {
  opacity: 1;
  transform: none;
}
#app .session-action-menu {
  position: fixed;
  inset: auto;
  margin: 0;
}
```

## Target

Occasional menus, purpose spatial consistency/preventing jarring changes. Use existing trigger-aware origins and shared `--ease-out: cubic-bezier(0.23, 1, 0.32, 1)`, entry 180ms and exit 140ms for opacity and transform; include matching `display` and `overlay` allow-discrete transitions. Initial scale .96, opacity 0; final transform none, opacity 1. Add @starting-style for `.menulist:popover-open`; retain existing SessionActions starting style. Preserve centered Sheet origins and upstream sizing.

## Repo conventions to follow

CSS tokens live in src/shelf.css, loaded last after glass/appearance. SelectMenu.vue sets dynamic transformOrigin; SessionActions.vue similarly places above/below. Existing @starting-style for action menu is the exemplar. Do not add a second easing vocabulary; set --ease-out to the exact curve in Shelf's root if needed.

## Steps

1. Add scoped popover transitions/entry starting style in src/shelf.css using the exact target values.
2. Add display/overlay exit retention to SessionActions; ensure opening/closing mid-transition retargets.
3. Preserve immediate keyboard, reduced-motion and performance behavior; explicitly cover both `.menulist` and `#app .session-action-menu` with needed overrides. Hover remains capability-gated.

## Boundaries

No markup changes, dependencies, playback/map clocks, chart/list/nav animation, or vendor Sheet sizing changes. If current snippets drift, report rather than improvise.

## Verification

npm run build, appropriate Shelf browser smoke. Browser getAnimations must report opening SelectMenu and closing SessionActions transitions; rapid open/close must settle without stuck top layers. At 20% playback inspect origin and final opacity/scale. Keyboard, reduced motion and performance must not move either menu. Real Safari remains a follow-up.

## Execution result

Implemented in `db71a05`; verified with production typecheck/build and actual-browser `make motion-smoke`. Native keyboard/reduced/performance behavior, rapid reversal, zero measured divider discontinuity and immediate mocked export handoff passed. Physical Safari remains unverified.
