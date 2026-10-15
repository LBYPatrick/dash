<script setup lang="ts">
import { useTranslation } from 'i18next-vue';
import { HardDrive, ChevronDown } from '../icons';
const { t } = useTranslation();
defineProps<{ name: string }>();
defineEmits<{ switch: [] }>();
</script>
<template>
  <button
    type="button"
    class="device-switcher"
    :aria-label="t('device.switchDevice')"
    aria-haspopup="dialog"
    :title="t('device.currentDevice', { name })"
    @click="$emit('switch')"
  >
    <HardDrive :size="17" /><span>{{ name }}</span
    ><ChevronDown :size="13" />
  </button>
</template>
