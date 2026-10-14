<script setup lang="ts">
import { useTranslation } from 'i18next-vue';
import { computed, onBeforeUnmount, onMounted, ref } from 'vue';
import { readStored, writeStored } from '../storage';
const { t } = useTranslation();
const width = defineModel<number>({ required: true });
const resetting = defineModel<boolean>('resetting', { default: false });
const viewport = ref(innerWidth);
const maximum = computed(() =>
  Math.max(240, Math.min(480, viewport.value - 640)),
);
let drag: { x: number; width: number; pointer: number } | undefined;
let timer: ReturnType<typeof setTimeout>;
const clamp = (value: number) => Math.max(240, Math.min(maximum.value, value));
function resize() {
  viewport.value = innerWidth;
  width.value = clamp(width.value);
}
function save() {
  writeStored('dash.sidebarWidth', width.value);
}
function start(event: PointerEvent) {
  if (!event.isPrimary || event.button !== 0) return;
  event.preventDefault();
  clearTimeout(timer);
  const actual = (event.currentTarget as HTMLElement)
    .parentElement!.querySelector('.sidebar')!
    .getBoundingClientRect().width;
  resetting.value = false;
  width.value = clamp(actual);
  drag = { x: event.clientX, width: width.value, pointer: event.pointerId };
  (event.currentTarget as HTMLElement).setPointerCapture(event.pointerId);
}
function move(event: PointerEvent) {
  if (drag?.pointer === event.pointerId)
    width.value = clamp(drag.width + event.clientX - drag.x);
}
function finish(event: PointerEvent) {
  if (drag?.pointer !== event.pointerId) return;
  drag = undefined;
  save();
}
function reset() {
  clearTimeout(timer);
  resetting.value = true;
  width.value = clamp(320);
  save();
  timer = setTimeout(() => (resetting.value = false), 300);
}
function key(event: KeyboardEvent) {
  if (event.key === 'Enter') {
    event.preventDefault();
    reset();
    return;
  }
  const target =
    event.key === 'ArrowLeft'
      ? width.value - 16
      : event.key === 'ArrowRight'
        ? width.value + 16
        : event.key === 'Home'
          ? 240
          : event.key === 'End'
            ? maximum.value
            : undefined;
  if (target === undefined) return;
  event.preventDefault();
  resetting.value = false;
  width.value = clamp(target);
  save();
}
onMounted(() => {
  const saved = readStored<unknown>('dash.sidebarWidth', 320);
  width.value = clamp(
    typeof saved === 'number' && Number.isFinite(saved) ? saved : 320,
  );
  window.addEventListener('resize', resize);
});
onBeforeUnmount(() => {
  clearTimeout(timer);
  window.removeEventListener('resize', resize);
});
</script>
<template>
  <div
    class="sidebar-resize-handle"
    role="separator"
    tabindex="0"
    :aria-label="t('sidebar.resizeRecordingSidebar')"
    aria-orientation="vertical"
    :aria-valuemin="240"
    :aria-valuemax="maximum"
    :aria-valuenow="Math.round(width)"
    :title="t('panels.dragToResizeDoubleClickToReset')"
    @pointerdown="start"
    @pointermove="move"
    @pointerup="finish"
    @pointercancel="finish"
    @lostpointercapture="finish"
    @dblclick="reset"
    @keydown="key"
  >
    <span aria-hidden="true"></span>
  </div>
</template>
