<script setup lang="ts">
import { useTranslation } from 'i18next-vue';
import { useDismiss } from '../vendor/shelf/useDismiss';
import { nextTick, onBeforeUnmount, ref, useId } from 'vue';
import { MoreHorizontal } from '../icons';
const { t } = useTranslation();
const props = defineProps<{ label?: string }>();
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
async function toggle() {
  if (menu.value?.matches(':popover-open')) {
    close();
    return;
  }
  position();
  menu.value?.showPopover();
  opened.value = true;
  window.addEventListener('resize', position);
  window.addEventListener('scroll', position, true);
  await nextTick();
  menu.value
    ?.querySelector<HTMLButtonElement>('button:not([disabled])')
    ?.focus({ preventScroll: true });
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
  if (!['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) return;
  event.preventDefault();
  const wasOpen = opened.value;
  if (!wasOpen) await toggle();
  await nextTick();
  const buttons = [
    ...menu.value!.querySelectorAll<HTMLButtonElement>(
      'button:not([disabled])',
    ),
  ];
  const index = buttons.indexOf(document.activeElement as HTMLButtonElement);
  const target =
    event.key === 'Home'
      ? 0
      : event.key === 'End'
        ? buttons.length - 1
        : !wasOpen || index < 0
          ? event.key === 'ArrowUp'
            ? buttons.length - 1
            : 0
          : (index + (event.key === 'ArrowDown' ? 1 : -1) + buttons.length) %
            buttons.length;
  buttons[target]?.focus({ preventScroll: true });
}
useDismiss(opened, close);
onBeforeUnmount(detach);
defineExpose({ close });
</script>
<template>
  <div class="session-actions">
    <button
      :id="`${menuId}-trigger`"
      ref="trigger"
      type="button"
      class="icon-button"
      :aria-label="props.label || t('actions.sessionActions')"
      :aria-controls="menuId"
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
      role="group"
      :aria-labelledby="`${menuId}-trigger`"
      class="session-action-menu"
      :style="placement"
      @toggle="toggled"
      @keydown="keyboard"
    >
      <slot />
    </div>
  </div>
</template>
