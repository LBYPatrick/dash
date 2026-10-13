# Shelf popup widget

`Sheet.vue` and `useDismiss.ts` are copied from Shelf at commit
`30838b2c9a69e288f6b032f7e84e5c768862ed4e`:

- `src/renderer/components/ui/Sheet.vue`
- `src/renderer/composables/useDismiss.ts`

Their sizing, bounded measurement correction, transitions, scroll-edge masks,
focus handling and dismissal stack are upstream code, not an independent Dash
implementation. The [MIT notice](../../../LICENSES/Shelf.txt) applies.

Dash changes import paths, teleports into `#app` so application styles apply,
replaces the translated close label with a prop, and adds integration classes
for existing styles/tests. Prettier follows Dash's formatting. The compatibility
adapter in `src/components/Sheet.vue` exposes `showModal()` / `close()` and the
close event expected by callers. No Electron or translation runtime is needed.

Selectors and action menus register with the same dismissal stack, so Escape
closes the most recently opened overlay first.
