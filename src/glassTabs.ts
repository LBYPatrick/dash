import type { ObjectDirective } from 'vue';
// One shared highlight keeps tab changes spatially continuous, including
// the vertical mobile navigation. Observers stop when their surface unmounts.
const cleanup = new WeakMap<HTMLElement, () => void>();
export const vGlassTabs: ObjectDirective<HTMLElement> = {
  mounted(element) {
    element.classList.add('glass-tabs');
    const update = () => {
      const active = element.querySelector<HTMLElement>(
        ':scope > button.selected, :scope > button.chosen',
      );
      const bounds = element.getBoundingClientRect();
      if (!active || !bounds.width || !bounds.height) return;
      const rect = active.getBoundingClientRect();
      const inset = [
        rect.top - bounds.top,
        bounds.right - rect.right,
        bounds.bottom - rect.bottom,
        rect.left - bounds.left,
      ];
      element.style.setProperty(
        '--selection-inset',
        `${inset.map((value) => `${Math.max(0, value)}px`).join(' ')} round 22px`,
      );
    };
    const keyboard = () => {
      element.dataset.keyboard = '';
    };
    const pointer = () => {
      delete element.dataset.keyboard;
    };
    element.addEventListener('keydown', keyboard);
    element.addEventListener('pointerdown', pointer);
    const size = new ResizeObserver(update);
    size.observe(element);
    const selection = new MutationObserver(update);
    selection.observe(element, {
      subtree: true,
      attributes: true,
      attributeFilter: ['class'],
    });
    update();
    cleanup.set(element, () => {
      size.disconnect();
      selection.disconnect();
      element.removeEventListener('keydown', keyboard);
      element.removeEventListener('pointerdown', pointer);
    });
  },
  unmounted(element) {
    cleanup.get(element)?.();
    cleanup.delete(element);
  },
};
