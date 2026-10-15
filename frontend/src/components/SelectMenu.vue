<script setup lang="ts" generic="T extends string | number">
import { useTranslation } from 'i18next-vue';
// Shelf-style anchored list. Native popover puts it above clipping ancestors
// and lets Escape dismiss the menu before its containing modal sheet.
import { useDismiss } from '../vendor/shelf/useDismiss';
import { computed, nextTick, onBeforeUnmount, ref, useId } from 'vue';
import { Check, ChevronDown } from '../icons';
const { t } = useTranslation();
const props = defineProps<{
  options: readonly { value: T; label: string; disabled?: boolean }[];
  disabled?: boolean;
  ariaLabel?: string;
  id?: string;
}>();
const model = defineModel<T>({ required: true });
const trigger = ref<HTMLButtonElement>();
const list = ref<HTMLDivElement>();
const opened = ref(false);
const active = ref(0);
const listId = useId();
const selected = computed(() =>
  props.options.findIndex((option) => option.value === model.value),
);
const label = computed(
  () => props.options[selected.value]?.label || t('select.choose'),
);
const placement = ref<Record<string, string>>({});
let anchorObserver: ResizeObserver | undefined;
let query = '';
let timer: ReturnType<typeof setTimeout>;
function position() {
  if (!trigger.value || !list.value) return;
  const box = trigger.value.getBoundingClientRect();
  const width = Math.min(
    Math.max(box.width, 200),
    document.documentElement.clientWidth - 16,
  );
  const roomBelow = innerHeight - box.bottom - 8;
  const roomAbove = box.top - 8;
  const above =
    roomBelow < Math.min(props.options.length * 32 + 8, 260) &&
    roomAbove > roomBelow;
  const maxHeight = Math.max(40, Math.min(320, above ? roomAbove : roomBelow));
  placement.value = {
    width: `${width}px`,
    left: `${Math.max(8, Math.min(box.left, document.documentElement.clientWidth - width - 8))}px`,
    maxHeight: `${maxHeight}px`,
    ...(above
      ? { bottom: `${innerHeight - box.top + 4}px`, top: 'auto' }
      : { top: `${box.bottom + 4}px`, bottom: 'auto' }),
    transformOrigin: above ? 'bottom center' : 'top center',
  };
}
async function show() {
  if (props.disabled || !props.options.some((option) => !option.disabled))
    return;
  active.value =
    selected.value >= 0 && !props.options[selected.value].disabled
      ? selected.value
      : props.options.findIndex((option) => !option.disabled);
  position();
  list.value?.showPopover();
  opened.value = true;
  anchorObserver?.disconnect();
  anchorObserver = new ResizeObserver(position);
  const container =
    trigger.value?.closest('dialog, [role="dialog"]') || trigger.value;
  if (container) anchorObserver.observe(container);
  window.addEventListener('resize', position);
  window.addEventListener('scroll', position, true);
  await nextTick();
  revealActive();
}
function hide() {
  list.value?.hidePopover();
  opened.value = false;
  detach();
}
function detach() {
  anchorObserver?.disconnect();
  window.removeEventListener('resize', position);
  window.removeEventListener('scroll', position, true);
}
function toggled() {
  opened.value = !!list.value?.matches(':popover-open');
  if (!opened.value) detach();
}
function choose(index: number) {
  const option = props.options[index];
  if (!option || option.disabled) return;
  model.value = option.value;
  hide();
  trigger.value?.focus({ preventScroll: true });
}
function move(delta: number) {
  if (!opened.value) {
    void show();
    return;
  }
  for (let steps = 0; steps < props.options.length; steps++) {
    active.value =
      (active.value + delta + props.options.length) % props.options.length;
    if (!props.options[active.value].disabled) break;
  }
  void nextTick(revealActive);
}
function revealActive() {
  const row = list.value?.querySelector<HTMLElement>('[data-active="true"]');
  if (!row || !list.value) return;
  if (row.offsetTop < list.value.scrollTop)
    list.value.scrollTop = row.offsetTop;
  else if (
    row.offsetTop + row.offsetHeight >
    list.value.scrollTop + list.value.clientHeight
  )
    list.value.scrollTop =
      row.offsetTop + row.offsetHeight - list.value.clientHeight;
}
function keys(event: KeyboardEvent) {
  if (event.key === 'Escape' && opened.value) {
    event.preventDefault();
    event.stopPropagation();
    hide();
    return;
  }
  if (event.key === 'Tab') {
    hide();
    return;
  }
  if (event.key === 'Home' || event.key === 'End') {
    event.preventDefault();
    if (!opened.value) void show();
    active.value =
      event.key === 'Home'
        ? props.options.findIndex((option) => !option.disabled)
        : props.options.findLastIndex((option) => !option.disabled);
    void nextTick(revealActive);
    return;
  }
  if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
    event.preventDefault();
    move(event.key === 'ArrowDown' ? 1 : -1);
    return;
  }
  if (event.key === 'Enter' || event.key === ' ') {
    event.preventDefault();
    if (opened.value) choose(active.value);
    else void show();
    return;
  }
  if (event.key.length === 1 && !event.ctrlKey && !event.metaKey) {
    query += event.key.toLowerCase();
    clearTimeout(timer);
    timer = setTimeout(() => (query = ''), 600);
    const index = props.options.findIndex(
      (option) =>
        !option.disabled && option.label.toLowerCase().startsWith(query),
    );
    if (index >= 0) {
      event.preventDefault();
      if (!opened.value) void show();
      active.value = index;
      void nextTick(revealActive);
    }
  }
}
useDismiss(opened, hide);
onBeforeUnmount(() => {
  detach();
  clearTimeout(timer);
});
</script>
<template>
  <div class="selectmenu">
    <button
      :id="id"
      ref="trigger"
      type="button"
      class="select-trigger"
      role="combobox"
      :disabled="disabled"
      :aria-label="ariaLabel"
      :aria-expanded="opened"
      aria-haspopup="listbox"
      :popovertarget="listId"
      :aria-controls="listId"
      :aria-activedescendant="opened ? `${listId}-${active}` : undefined"
      @click.prevent="opened ? hide() : show()"
      @keydown="keys"
    >
      <span>{{ label }}</span
      ><ChevronDown :size="12" />
    </button>
    <div
      :id="listId"
      ref="list"
      popover="auto"
      class="menulist"
      :style="placement"
      role="listbox"
      :aria-label="ariaLabel"
      @toggle="toggled"
    >
      <div
        v-for="(option, index) in options"
        :id="`${listId}-${index}`"
        :key="option.value"
        role="option"
        class="menulist-option"
        :aria-selected="option.value === model"
        :aria-disabled="option.disabled || undefined"
        :data-active="active === index"
        @pointerenter="!option.disabled && (active = index)"
        @click.prevent.stop="choose(index)"
      >
        <Check
          :size="12"
          :style="{ visibility: option.value === model ? 'visible' : 'hidden' }"
        /><span>{{ option.label }}</span>
      </div>
    </div>
  </div>
</template>
