<script setup lang="ts">
// Compatibility adapter; rendering, motion, sizing and focus belong to Shelf.
import { ref, watch } from 'vue';
import ShelfSheet from '../vendor/shelf/Sheet.vue';
defineOptions({ inheritAttrs: false });
const props = defineProps<{
  title: string;
  closeLabel: string;
  wide?: boolean;
}>();
const emit = defineEmits<{ close: [event: Event]; cancel: [event: Event] }>();
const open = ref(false);
function showModal() {
  open.value = true;
}
function close() {
  open.value = false;
}
watch(open, (value) => {
  if (!value) emit('close', new Event('close'));
});
defineExpose({ showModal, close });
</script>
<template>
  <ShelfSheet
    v-model="open"
    v-bind="$attrs"
    :title="props.title"
    :wide="props.wide"
    :close-label="props.closeLabel"
  >
    <slot />
  </ShelfSheet>
</template>
