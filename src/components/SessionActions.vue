<script setup lang="ts">
import { useDismiss } from '../vendor/shelf/useDismiss';
import { nextTick, onBeforeUnmount, ref, useId } from 'vue';
import { MoreHorizontal } from '../icons';
const props = withDefaults(defineProps<{ label?: string }>(), {
  label: 'Session actions',
});
const trigger = ref<HTMLButtonElement>();
const menu = ref<HTMLDivElement>();
const menuId = useId();
const opened = ref(false);
const placement = ref<Record<string, string>>({});
function position() {
  if (!trigger.value) return;
  const box = trigger.value.getBoundingClientRect();
  const above =
    innerHeight - box.bottom < 260 && box.top > innerHeight - box.bottom;
  placement.value = {
    left: `${Math.max(8, Math.min(box.right - 204, document.documentElement.clientWidth - 212))}px`,
    maxHeight: `${Math.max(80, (above ? box.top : innerHeight - box.bottom) - 12)}px`,
    ...(above
      ? {
          top: 'auto',
          bottom: `${innerHeight - box.top + 4}px`,
          transformOrigin: 'bottom right',
        }
      : {
          top: `${box.bottom + 4}px`,
          bottom: 'auto',
          transformOrigin: 'top right',
        }),
  };
}
function close() {
  menu.value?.hidePopover();
  opened.value = false;
  detach();
  trigger.value?.focus({ preventScroll: true });
}
function detach() {
  window.removeEventListener('resize', position);
  window.removeEventListener('scroll', position, true);
}
function toggle() {
  if (menu.value?.matches(':popover-open')) {
    close();
    return;
  }
  position();
  menu.value?.showPopover();
  opened.value = true;
  window.addEventListener('resize', position);
  window.addEventListener('scroll', position, true);
}
function toggled() {
  opened.value = !!menu.value?.matches(':popover-open');
  if (!opened.value) detach();
}
async function keyboard(event: KeyboardEvent) {
  if (event.key === 'Escape') {
    event.preventDefault();
    event.stopPropagation();
    close();
    trigger.value?.focus();
    return;
  }
  if (event.key !== 'ArrowDown' && event.key !== 'ArrowUp') return;
  event.preventDefault();
  if (!opened.value) toggle();
  await nextTick();
  const buttons = [
    ...menu.value!.querySelectorAll<HTMLButtonElement>(
      'button:not([disabled])',
    ),
  ];
  const index = buttons.indexOf(document.activeElement as HTMLButtonElement);
  buttons[
    (index + (event.key === 'ArrowDown' ? 1 : -1) + buttons.length) %
      buttons.length
  ]?.focus();
}
useDismiss(opened, close);
onBeforeUnmount(detach);
defineExpose({ close });
</script>
<template>
  <div class="session-actions">
    <button
      ref="trigger"
      type="button"
      class="icon-button"
      :aria-label="props.label"
      aria-haspopup="true"
      :aria-expanded="opened"
      :popovertarget="menuId"
      @click.prevent="toggle"
      @keydown="keyboard"
    >
      <MoreHorizontal :size="20" />
    </button>
    <div
      :id="menuId"
      ref="menu"
      popover="auto"
      class="session-action-menu"
      :style="placement"
      @toggle="toggled"
      @keydown="keyboard"
    >
      <slot />
    </div>
  </div>
</template>
