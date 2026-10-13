<script setup lang="ts">
import { vGlassTabs } from '../glassTabs';
import DeviceSwitcher from './DeviceSwitcher.vue';
withDefaults(
  defineProps<{
    view: 'drives' | 'overview' | 'live';
    deviceName: string;
    showDevice?: boolean;
  }>(),
  { showDevice: true },
);
defineEmits<{ navigate: [view: 'drives' | 'overview' | 'live']; device: [] }>();
const destinations = [
  { id: 'overview', label: 'Overview' },
  { id: 'drives', label: 'Recordings' },
  { id: 'live', label: 'Live' },
] as const;
</script>
<template>
  <div class="workspace-navigation">
    <nav v-glass-tabs class="workspace-nav" aria-label="Workspace">
      <button
        v-for="destination in destinations"
        :key="destination.id"
        :class="{ chosen: view === destination.id }"
        :aria-current="view === destination.id ? 'page' : undefined"
        @click="$emit('navigate', destination.id)"
      >
        {{ destination.label }}
      </button>
    </nav>
    <DeviceSwitcher
      v-if="showDevice"
      :name="deviceName"
      @switch="$emit('device')"
    />
  </div>
</template>
