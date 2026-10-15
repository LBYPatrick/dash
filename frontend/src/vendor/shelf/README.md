# Shelf popup widget

`Sheet.vue` and `useDismiss.ts` are copied from Shelf at commit
`30838b2c9a69e288f6b032f7e84e5c768862ed4e`:

- `src/renderer/components/ui/Sheet.vue`
- `src/renderer/composables/useDismiss.ts`

Their sizing, bounded measurement correction, transitions, scroll-edge masks,
focus handling and dismissal stack are upstream code, not an independent Dash
implementation. The [MIT notice](../../../../LICENSES/Shelf.txt) applies.

Dash changes import paths, teleports into `#app` so application styles apply,
replaces the translated close label with a prop, and adds integration classes
for existing styles/tests. Oxfmt follows Dash's formatting. The compatibility
adapter in `frontend/src/components/Sheet.vue` exposes `showModal()` / `close()` and the
close event expected by callers. No Electron runtime is needed. Dash now uses the same i18next/i18next-vue translation stack as Shelf; translated close labels still flow through the adapter prop.

Selectors and action menus register with the same dismissal stack, so Escape
closes the most recently opened overlay first.

`HoverTip.vue` and `hoverTip.ts` are also copied from the same Shelf commit:
`src/renderer/components/chrome/HoverTip.vue` and `src/renderer/lib/hoverTip.ts`.
Dash changes the import path, skips hover on touch input, guards hidden/detached
triggers, supports below-toolbar placement, dismisses labels on scroll/resize/Escape, and removes transitions in
performance/reduced-motion modes. The same MIT notice applies.

The notification system is ported from that same commit:
`stores/toasts.ts`, `components/chrome/ToastStack.vue`,
`components/chrome/ToastItem.vue`, `composables/useDrag.ts`, and `styles/motion.ts`.
It retains Shelf's stacked cards, interruptible transitions, swipe dismissal,
actions, replacement by ID, and expiry paused during hover, focus, dragging or
window blur. Dash uses a Vue singleton instead of Pinia, omits Electron desktop
notifications, and adapts icons, translation keys, spacing tokens, safe areas,
coarse-pointer targets, reduced motion and performance mode to the browser.

Dash strengthens notification text/icon contrast, removes perimeter outlines in favor of semantic status marks, and retains upstream notification curves and timings. The notification region clears playback controls and follows fullscreen.

`accents.ts`, `color.ts`, and the palette-only subset of `styles/theme.ts` are also copied from Shelf at the same commit. Dash keeps its eight named presets, legacy-value validation, OKLCH neutral derivation and contrast-aware primary colors; it omits editor, density and Electron material controls.

Browser accessibility adaptations isolate the topmost sheet with `inert` and
restore background/parent state before returning focus. Toast exits preserve
their screen position while remaining cards close the gap. Interrupted swipes
return to rest; release velocity expires after a stationary hold. These fixes
retain Shelf’s visual vocabulary and are covered by browser acceptance tests.
