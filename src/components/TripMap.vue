<script setup lang="ts">
import { defineAsyncComponent, ref } from 'vue';
import { mapboxToken } from '../map';
import type { Position } from '../indexer';
defineProps<{ points: Position[]; position?: Position }>();
const following = ref(true);
// Keep the credential-free renderer small. The Mapbox renderer is loaded only
// for deployments that explicitly configure a browser token in .env.
const Renderer = defineAsyncComponent(
  mapboxToken
    ? () => import('./MapboxTripMap.vue')
    : () => import('./OpenStreetMap.vue'),
);
</script>
<template>
  <Renderer
    :points="points"
    :position="position"
    v-model:following="following"
  />
</template>
