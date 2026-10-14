<script setup lang="ts">
import { computed, onMounted, onBeforeUnmount, ref } from 'vue';
import { readStored, writeStored } from '../storage';
const storageKey = 'dash.videoMapRatio';
const saved = readStored<unknown>(storageKey, null);
const ratio = ref(
  typeof saved === 'number' && Number.isFinite(saved) && saved > 0 && saved < 1
    ? saved
    : 0.7,
);
const panels = ref<HTMLDivElement>();
const available = ref(760);
const dragging = ref(false);
const resetting = ref(false);
let resetTimer: ReturnType<typeof setTimeout>;
const usable = computed(() => Math.max(1, available.value - 24));
const minimum = computed(() => Math.min(320, usable.value * 0.6));
const maximum = computed(() => Math.max(minimum.value, usable.value - 220));
const clamp = (value: number) =>
  Math.min(maximum.value, Math.max(minimum.value, value));
const width = computed(() => clamp(usable.value * ratio.value));
let observer: ResizeObserver;
let drag: { x: number; width: number; pointer: number } | undefined;
function change(value: number) {
  ratio.value = clamp(value) / usable.value;
}
function persist() {
  writeStored(storageKey, ratio.value);
}
function start(event: PointerEvent) {
  if (!event.isPrimary || event.button !== 0) return;
  event.preventDefault();
  clearTimeout(resetTimer);
  // A reset may still be interpolating: begin at the visible divider, not its
  // destination, before removing the transition and capturing the pointer.
  available.value = panels.value!.getBoundingClientRect().width;
  const actual = panels
    .value!.querySelector<HTMLElement>('.session-video')!
    .getBoundingClientRect().width;
  const startWidth = clamp(actual);
  ratio.value = startWidth / usable.value;
  resetting.value = false;
  drag = { x: event.clientX, width: startWidth, pointer: event.pointerId };
  dragging.value = true;
  (event.currentTarget as HTMLElement).setPointerCapture(event.pointerId);
}
function move(event: PointerEvent) {
  if (drag?.pointer === event.pointerId)
    change(drag.width + event.clientX - drag.x);
}
function finish(event: PointerEvent) {
  if (drag?.pointer !== event.pointerId) return;
  persist();
  drag = undefined;
  dragging.value = false;
}
function reset() {
  clearTimeout(resetTimer);
  resetting.value = true;
  resetTimer = setTimeout(() => (resetting.value = false), 300);
  ratio.value = 0.7;
  persist();
}
function keyboard(event: KeyboardEvent) {
  if (event.key === 'Enter') {
    event.preventDefault();
    reset();
    return;
  }
  const value =
    event.key === 'ArrowLeft'
      ? width.value - 16
      : event.key === 'ArrowRight'
        ? width.value + 16
        : event.key === 'Home'
          ? minimum.value
          : event.key === 'End'
            ? maximum.value
            : undefined;
  if (value === undefined) return;
  event.preventDefault();
  change(value);
  persist();
}
onMounted(() => {
  observer = new ResizeObserver(() => {
    available.value = panels.value!.getBoundingClientRect().width;
  });
  available.value = panels.value!.getBoundingClientRect().width;
  observer.observe(panels.value!);
});
onBeforeUnmount(() => {
  clearTimeout(resetTimer);
  observer?.disconnect();
});
</script>
<template>
  <div
    ref="panels"
    class="session-layout"
    :data-resizing="dragging || undefined"
    :data-resetting="resetting || undefined"
    :style="{ '--video-panel-width': `${width}px` }"
  >
    <slot name="video"></slot>
    <div
      class="media-resize-handle"
      role="separator"
      tabindex="0"
      aria-label="Resize video and map"
      aria-orientation="vertical"
      :aria-valuemin="Math.round(minimum)"
      :aria-valuemax="Math.round(maximum)"
      :aria-valuenow="Math.round(width)"
      :aria-valuetext="`${Math.round((width / usable) * 100)}% video`"
      title="Drag to resize · Double-click to reset"
      @pointerdown="start"
      @pointermove="move"
      @pointerup="finish"
      @pointercancel="finish"
      @lostpointercapture="finish"
      @dblclick="reset"
      @keydown="keyboard"
    >
      <span aria-hidden="true"></span>
    </div>
    <slot name="data"></slot>
  </div>
</template>
