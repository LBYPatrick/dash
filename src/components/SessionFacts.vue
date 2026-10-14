<script setup lang="ts">
import { computed } from 'vue';
import type { Telemetry } from '../indexer';
const props = defineProps<{ metadata: Telemetry[]; total: number }>();
const duration = computed(
  () =>
    props.metadata.reduce((sum, meta) => sum + (meta.duration || 0), 0) / 60,
);
const hasDistance = computed(() =>
  props.metadata.some(
    (meta) => (meta.distanceSamples || 0) > 0 || (meta.points?.length || 0) > 1,
  ),
);
const hasDuration = computed(() =>
  props.metadata.some((meta) => typeof meta.duration === 'number'),
);
const distance = computed(() =>
  props.metadata.reduce((sum, meta) => sum + (meta.distanceKm || 0), 0),
);
</script>
<template>
  <div class="session-facts">
    <div class="section-heading">
      <div>
        <h2>Metadata</h2>
        <p>{{ metadata.length }} / {{ total }} segments decoded</p>
      </div>
    </div>
    <div class="stat-grid">
      <div>
        <small>Measured distance</small
        ><strong
          >{{ hasDistance ? distance.toFixed(1) : '—' }} <span>km</span></strong
        >
      </div>
      <div>
        <small>Recorded time</small
        ><strong
          >{{ hasDuration ? duration.toFixed(1) : '—' }}
          <span>min</span></strong
        >
      </div>
      <div>
        <small>Vehicle</small
        ><strong class="vehicle-name">{{
          metadata.find((meta) => meta.car)?.car || 'Not recorded'
        }}</strong>
      </div>
    </div>
  </div>
</template>
