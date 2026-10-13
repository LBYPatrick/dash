<script setup lang="ts">
import { HardDrive, ChevronDown } from '../icons';
defineProps<{ name: string }>();
defineEmits<{ switch: [] }>();
</script>
<template>
  <button
    type="button"
    class="device-switcher"
    aria-label="Switch device"
    aria-haspopup="dialog"
    :title="`Current device: ${name}`"
    @click="$emit('switch')"
  >
    <HardDrive :size="17" /><span>{{ name }}</span
    ><ChevronDown :size="13" />
  </button>
</template>
