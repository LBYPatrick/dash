<script setup lang="ts">
import { useTranslation } from 'i18next-vue';
import { formatNumber } from '../i18n';
import { computed } from 'vue';
import type { Telemetry } from '../indexer';
const { t } = useTranslation();
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
        <h2>{{ t('app.metadata') }}</h2>
        <p>{{ t('facts.decoded', { count: metadata.length, total }) }}</p>
      </div>
    </div>
    <div class="stat-grid">
      <div>
        <small>{{ t('facts.measuredDistance') }}</small
        ><strong
          >{{
            hasDistance
              ? formatNumber(distance, {
                  minimumFractionDigits: 1,
                  maximumFractionDigits: 1,
                })
              : '—'
          }}
          <span>{{ t('facts.km') }}</span></strong
        >
      </div>
      <div>
        <small>{{ t('chart.recordedTime') }}</small
        ><strong
          >{{
            hasDuration
              ? formatNumber(duration, {
                  minimumFractionDigits: 1,
                  maximumFractionDigits: 1,
                })
              : '—'
          }}
          <span>{{ t('facts.min') }}</span></strong
        >
      </div>
      <div>
        <small>{{ t('facts.vehicle') }}</small
        ><strong class="vehicle-name">{{
          metadata.find((meta) => meta.car)?.car || t('facts.notRecorded')
        }}</strong>
      </div>
    </div>
  </div>
</template>
