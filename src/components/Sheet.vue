<script setup lang="ts">
// Adapted from Shelf's MIT-licensed Sheet: a centred, content-sized surface.
// Native dialog supplies top-layer stacking, inertness and keyboard focus.
import { nextTick, onBeforeUnmount, onMounted, ref, useId } from 'vue';
import { X } from '../icons';
const props = defineProps<{
  title: string;
  closeLabel: string;
  wide?: boolean;
}>();
const emit = defineEmits<{ close: [event: Event]; cancel: [event: Event] }>();
const dialog = ref<HTMLDialogElement>();
const measure = ref<HTMLDivElement>();
const head = ref<HTMLElement>();
const height = ref<number>();
const overflowing = ref(false);
const titleId = useId();
let observer: ResizeObserver;
let frame = 0;
let active = false;
let opener: HTMLElement | null;
function resize() {
  if (!active || !measure.value || !head.value || !dialog.value) return;
  const natural = Math.ceil(
    head.value.getBoundingClientRect().height +
      measure.value.getBoundingClientRect().height +
      20,
  );
  const ceiling = Math.min(
    innerHeight * 0.92,
    Math.max(innerHeight * 0.8, 480),
  );
  overflowing.value = natural > ceiling + 4;
  const target = overflowing.value ? Math.floor(ceiling) : natural;
  if (height.value !== target) height.value = target;
}
function queueResize() {
  cancelAnimationFrame(frame);
  frame = requestAnimationFrame(resize);
}
async function showModal() {
  if (dialog.value?.open) return;
  opener = document.activeElement as HTMLElement | null;
  height.value = undefined;
  active = true;
  dialog.value?.showModal();
  await nextTick();
  resize();
}
function close() {
  active = false;
  dialog.value?.close();
}
function canceled(event: Event) {
  event.preventDefault();
  emit('cancel', event);
  close();
}
function closed(event: Event) {
  active = false;
  opener?.focus({ preventScroll: true });
  emit('close', event);
}
function outside(event: MouseEvent) {
  if (event.target !== dialog.value) return;
  const bounds = dialog.value.getBoundingClientRect();
  if (
    event.clientX < bounds.left ||
    event.clientX > bounds.right ||
    event.clientY < bounds.top ||
    event.clientY > bounds.bottom
  )
    close();
}
onMounted(() => {
  observer = new ResizeObserver(queueResize);
  if (measure.value) observer.observe(measure.value);
  if (head.value) observer.observe(head.value);
  window.addEventListener('resize', queueResize);
});
onBeforeUnmount(() => {
  active = false;
  observer?.disconnect();
  cancelAnimationFrame(frame);
  window.removeEventListener('resize', queueResize);
});
defineExpose({ showModal, close });
</script>
<template>
  <dialog
    ref="dialog"
    class="modal shelf-sheet"
    :class="{ 'shelf-sheet--wide': props.wide }"
    :style="{ height: height === undefined ? undefined : `${height}px` }"
    :aria-labelledby="titleId"
    @cancel="canceled"
    @close="closed"
    @click="outside"
    @transitionend="queueResize"
  >
    <header ref="head" class="shelf-sheet-head">
      <h2 :id="titleId">{{ title }}</h2>
      <button
        type="button"
        class="shelf-sheet-close"
        :aria-label="closeLabel"
        @click="close"
      >
        <X :size="16" />
      </button>
    </header>
    <section
      class="shelf-sheet-body"
      :class="{ 'shelf-sheet-body--scrolls': overflowing }"
    >
      <div ref="measure" class="shelf-sheet-measure"><slot /></div>
    </section>
  </dialog>
</template>
