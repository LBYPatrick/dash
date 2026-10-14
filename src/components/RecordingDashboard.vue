<script setup lang="ts">
import { useTranslation } from 'i18next-vue';
import { locale, formatNumber } from '../i18n';
import { computed, ref, watch } from 'vue';
import type { Drive } from '../library';
import type { IndexedSegment } from '../indexer';
import {
  summarizeDrives,
  periodDrives,
  activityBuckets,
  activityTotals,
  describeMetric,
  formatStatistic,
  validDate,
  type ActivityMetric,
  type ActivityGroup,
  type ActivityPeriod,
  type DistanceUnit,
} from '../statistics';
import SelectMenu from './SelectMenu.vue';
import ActivityChart from './ActivityChart.vue';
import '../dashboard.css';
import { readStored, writeStored } from '../storage';
import { vGlassTabs } from '../glassTabs';
const { t } = useTranslation();
const props = defineProps<{
  drives: Drive[];
  entries: Record<string, IndexedSegment>;
  demo: boolean;
}>();
const emit = defineEmits<{ open: [drive: Drive] }>();
const selected = defineModel<string>({ required: true });
const saved = readStored<Record<string, unknown>>('dash.dashboard', {});
const period = ref<ActivityPeriod>(
  [7, 30, 90, 'all'].includes(saved?.period as ActivityPeriod)
    ? (saved.period as ActivityPeriod)
    : 'all',
);
const group = ref<ActivityGroup>(
  ['drive', 'day', 'month'].includes(String(saved?.group))
    ? (saved.group as ActivityGroup)
    : 'day',
);
const metric = ref<ActivityMetric>(
  props.demo
    ? 'drives'
    : ['distance', 'duration', 'drives'].includes(String(saved?.metric))
      ? (saved.metric as ActivityMetric)
      : 'distance',
);
const unit = ref<DistanceUnit>(saved?.unit === 'mi' ? 'mi' : 'km');
const now = ref(Date.now());
const rows = computed(() => summarizeDrives(props.drives, props.entries));
const scope = computed(() => periodDrives(rows.value, period.value, now.value));
const totals = computed(() => activityTotals(scope.value));
const buckets = computed(() => activityBuckets(scope.value, group.value));
const chosen = computed(() =>
  buckets.value.find((b) => b.key === selected.value),
);
const matching = computed(() =>
  [...(chosen.value?.drives || scope.value)].sort(
    (a, b) => b.drive.date - a.drive.date,
  ),
);
const listPage = ref(0);
const visible = computed(() =>
  matching.value.slice(listPage.value * 20, (listPage.value + 1) * 20),
);
const undated = computed(
  () => scope.value.filter((d) => !validDate(d.drive.date)).length,
);
const fileDates = computed(
  () =>
    scope.value.filter((d) => !d.drive.dated && validDate(d.drive.date)).length,
);
const periodLabel = computed(() =>
  period.value === 'all'
    ? t('dashboard.allRetainedRecordings')
    : t('dashboard.lastDays', { count: period.value }),
);
watch([period, group], () => {
  selected.value = '';
  listPage.value = 0;
  now.value = Date.now();
});
watch(selected, () => (listPage.value = 0));
watch(
  () => matching.value.length,
  (n) => {
    listPage.value = Math.min(
      listPage.value,
      Math.max(0, Math.ceil(n / 20) - 1),
    );
  },
);
watch([period, group, metric, unit], () =>
  writeStored('dash.dashboard', {
    period: period.value,
    group: group.value,
    metric: metric.value,
    unit: unit.value,
  }),
);
const cards = computed(() => [
  {
    id: 'distance' as const,
    label: t('chart.gpsDistance'),
    value: totals.value.distanceParts
      ? formatStatistic(
          totals.value.distance * (unit.value === 'mi' ? 0.621371192237334 : 1),
        )
      : '—',
    unit: t(`units.${unit.value}`),
    coverage: t('common.coverage', {
      covered: totals.value.distanceParts,
      total: totals.value.total,
    }),
  },
  {
    id: 'duration' as const,
    label: t('chart.recordedTime'),
    value: totals.value.durationParts
      ? formatStatistic(
          totals.value.duration / (totals.value.duration >= 3600 ? 3600 : 60),
        )
      : '—',
    unit: totals.value.duration >= 3600 ? t('units.h') : t('facts.min'),
    coverage: t('common.coverage', {
      covered: totals.value.durationParts,
      total: totals.value.total,
    }),
  },
  {
    id: 'drives' as const,
    label: t('chart.drives'),
    value: formatNumber(scope.value.length),
    unit: '',
    coverage: t('dashboard.retainedOnDevice'),
  },
]);
function thumbnail(drive: Drive) {
  return (
    drive.segments.map((s) => props.entries[s.url]?.thumbnail).find(Boolean) ||
    (props.demo ? '/sample-road.svg' : undefined)
  );
}
function driveDate(drive: Drive) {
  return validDate(drive.date)
    ? new Date(drive.date).toLocaleDateString(locale.value, {
        month: 'short',
        day: 'numeric',
        year: 'numeric',
      })
    : t('dashboard.dateUnavailable');
}
function driveTime(drive: Drive) {
  return validDate(drive.date)
    ? new Date(drive.date).toLocaleTimeString(locale.value, {
        hour: 'numeric',
        minute: '2-digit',
      })
    : drive.id;
}
</script>
<template>
  <section
    class="overview recording-dashboard"
    :aria-label="t('dashboard.recordingStatistics')"
  >
    <header class="dashboard-heading">
      <div>
        <h1>{{ t('dashboard.recordingOverview') }}</h1>
        <p>{{ t('dashboard.localDates', { period: periodLabel }) }}</p>
      </div>
      <div class="dashboard-filters">
        <label
          >{{ t('chart.period')
          }}<SelectMenu
            v-model="period"
            :aria-label="t('dashboard.statisticsPeriod')"
            :options="[
              { value: 'all', label: t('dashboard.allRecordings') },
              { value: 7, label: t('dashboard.last7Days') },
              { value: 30, label: t('dashboard.last30Days') },
              { value: 90, label: t('dashboard.last90Days') },
            ]" /></label
        ><label
          >{{ t('dashboard.distanceUnit')
          }}<SelectMenu
            v-model="unit"
            :aria-label="t('dashboard.statisticsDistanceUnit')"
            :options="[
              { value: 'km', label: t('dashboard.kilometers') },
              { value: 'mi', label: t('dashboard.miles') },
            ]"
        /></label>
      </div>
    </header>
    <div
      class="dashboard-totals"
      role="group"
      :aria-label="t('dashboard.graphMetric')"
    >
      <button
        v-for="card in cards"
        :key="card.id"
        type="button"
        :aria-pressed="metric === card.id"
        :aria-label="t('dashboard.graphForMetric', { metric: card.label })"
        :class="{ selected: metric === card.id }"
        @click="metric = card.id"
      >
        <span>{{ card.label }}</span
        ><strong
          >{{ card.value }} <small>{{ card.unit }}</small></strong
        ><span class="stat-coverage">{{ card.coverage }}</span
        ><span v-if="metric === card.id" class="stat-selected">{{
          t('dashboard.graphing')
        }}</span>
      </button>
    </div>
    <p class="dashboard-coverage">
      {{
        t('dashboard.coverage', {
          covered: totals.covered,
          total: totals.total,
        })
      }}
      <span v-if="totals.covered < totals.total">{{
        t('dashboard.unindexedOrUnreadableLogsAreExcludedFrom')
      }}</span
      ><span v-else>{{
        t('dashboard.totalsDescribeTheseRecordingsNotAnOdometer')
      }}</span>
    </p>
    <p v-if="demo" class="dashboard-coverage">
      {{ t('dashboard.theSampleLibraryHasNoDecodedTelemetry') }}
    </p>
    <section
      class="dashboard-activity"
      aria-labelledby="driving-activity-title"
    >
      <header class="activity-controls">
        <h2 id="driving-activity-title">
          {{ t('dashboard.drivingActivity') }}
        </h2>
        <div
          v-glass-tabs
          class="segmented activity-group"
          role="group"
          :aria-label="t('dashboard.groupActivity')"
        >
          <button
            v-for="choice in [
              { id: 'drive', label: t('chart.drives') },
              { id: 'day', label: t('dashboard.days') },
              { id: 'month', label: t('dashboard.months') },
            ] as const"
            :key="choice.id"
            type="button"
            :class="{ selected: group === choice.id }"
            :aria-pressed="group === choice.id"
            @click="group = choice.id"
          >
            {{ choice.label }}
          </button>
        </div>
      </header>
      <ActivityChart
        v-model="selected"
        :buckets="buckets"
        :metric="metric"
        :unit="unit"
      />
      <p v-if="undated" class="dashboard-coverage">
        {{ t('dashboard.undated', { count: undated }) }}
      </p>
    </section>
    <section class="dashboard-drives" aria-labelledby="dashboard-drives-title">
      <header class="activity-controls">
        <div>
          <h2 id="dashboard-drives-title">
            {{ chosen?.label || t('dashboard.drivesInThisPeriod') }}
          </h2>
          <p role="status">
            {{
              chosen
                ? t('common.selectedDrives', { count: matching.length })
                : t('common.drives', { count: matching.length })
            }}<span v-if="chosen">
              · {{ describeMetric(chosen.drives, metric, unit) }}</span
            >
          </p>
        </div>
        <button v-if="selected" class="text-button" @click="selected = ''">
          {{ t('dashboard.clearSelection') }}
        </button>
      </header>
      <div v-if="!matching.length" class="dashboard-empty">
        <p>{{ t('dashboard.noRecordingsInThisPeriod') }}</p>
        <button v-if="period !== 'all'" class="button" @click="period = 'all'">
          {{ t('dashboard.showAllRecordings') }}
        </button>
      </div>
      <button
        v-for="item in visible"
        :key="item.drive.id"
        type="button"
        class="dashboard-drive"
        :aria-label="
          t('dashboard.reviewDrive', {
            date: driveDate(item.drive),
            time: driveTime(item.drive),
          })
        "
        @click="emit('open', item.drive)"
      >
        <img
          v-if="thumbnail(item.drive)"
          :src="thumbnail(item.drive)"
          alt=""
          loading="lazy"
        /><span v-else class="dashboard-thumbnail" aria-hidden="true"></span
        ><span class="dashboard-drive-date"
          ><strong
            >{{ driveDate(item.drive) }} · {{ driveTime(item.drive) }}</strong
          ><small
            >{{
              t('dashboard.indexed', {
                covered: item.covered,
                total: item.total,
              })
            }}{{ item.drive.dated ? '' : t('dashboard.fileDate') }}</small
          ></span
        ><span class="dashboard-drive-values"
          >{{ describeMetric([item], 'distance', unit)
          }}<small>{{ describeMetric([item], 'duration', unit) }}</small></span
        ><span class="dashboard-drive-arrow" aria-hidden="true">›</span>
      </button>
      <div v-if="matching.length > 20" class="dashboard-list-paging">
        <button class="button" :disabled="listPage === 0" @click="listPage--">
          {{ t('dashboard.previousDrives') }}</button
        ><span>{{
          t('common.range', {
            start: listPage * 20 + 1,
            end: Math.min(matching.length, (listPage + 1) * 20),
            total: matching.length,
          })
        }}</span
        ><button
          class="button"
          :disabled="(listPage + 1) * 20 >= matching.length"
          @click="listPage++"
        >
          {{ t('dashboard.nextDrives') }}
        </button>
      </div>
    </section>
    <details class="statistics-notes">
      <summary>{{ t('dashboard.howTotalsAreMeasured') }}</summary>
      <p>
        {{ t('dashboard.onlyRecordingsStillOnThisDeviceAre') }}
      </p>
      <p>
        {{ t('dashboard.eachDriveIsAssignedToItsLocal') }}
        {{ fileDates ? t('dashboard.fileDates', { count: fileDates }) : '' }}
        {{ t('dashboard.datesThatCannotBeReadAreExcluded') }}
      </p>
      <p>
        {{ t('dashboard.statisticsUseCachedIndexSummariesNewMetadata') }}
      </p>
    </details>
  </section>
</template>
