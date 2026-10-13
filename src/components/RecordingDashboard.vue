<script setup lang="ts">
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
    ? 'All retained recordings'
    : `Last ${period.value} days`,
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
    label: 'GPS distance',
    value: totals.value.distanceParts
      ? formatStatistic(
          totals.value.distance * (unit.value === 'mi' ? 0.621371192237334 : 1),
        )
      : '—',
    unit: unit.value,
    coverage: `${totals.value.distanceParts}/${totals.value.total} segments`,
  },
  {
    id: 'duration' as const,
    label: 'Recorded time',
    value: totals.value.durationParts
      ? formatStatistic(
          totals.value.duration / (totals.value.duration >= 3600 ? 3600 : 60),
        )
      : '—',
    unit: totals.value.duration >= 3600 ? 'h' : 'min',
    coverage: `${totals.value.durationParts}/${totals.value.total} segments`,
  },
  {
    id: 'drives' as const,
    label: 'Drives',
    value: scope.value.length.toLocaleString(),
    unit: '',
    coverage: 'Retained on device',
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
    ? new Date(drive.date).toLocaleDateString(undefined, {
        month: 'short',
        day: 'numeric',
        year: 'numeric',
      })
    : 'Date unavailable';
}
function driveTime(drive: Drive) {
  return validDate(drive.date)
    ? new Date(drive.date).toLocaleTimeString(undefined, {
        hour: 'numeric',
        minute: '2-digit',
      })
    : drive.id;
}
</script>
<template>
  <section
    class="overview recording-dashboard"
    aria-label="Recording statistics"
  >
    <header class="dashboard-heading">
      <div>
        <h1>Recording overview</h1>
        <p>{{ periodLabel }} · local dates</p>
      </div>
      <div class="dashboard-filters">
        <label
          >Period<SelectMenu
            v-model="period"
            aria-label="Statistics period"
            :options="[
              { value: 'all', label: 'All recordings' },
              { value: 7, label: 'Last 7 days' },
              { value: 30, label: 'Last 30 days' },
              { value: 90, label: 'Last 90 days' },
            ]" /></label
        ><label
          >Distance unit<SelectMenu
            v-model="unit"
            aria-label="Statistics distance unit"
            :options="[
              { value: 'km', label: 'Kilometers' },
              { value: 'mi', label: 'Miles' },
            ]"
        /></label>
      </div>
    </header>
    <div class="dashboard-totals" role="group" aria-label="Graph metric">
      <button
        v-for="card in cards"
        :key="card.id"
        type="button"
        :aria-pressed="metric === card.id"
        :aria-label="`Graph ${card.label.toLowerCase()}`"
        :class="{ selected: metric === card.id }"
        @click="metric = card.id"
      >
        <span>{{ card.label }}</span
        ><strong
          >{{ card.value }} <small>{{ card.unit }}</small></strong
        ><span class="stat-coverage">{{ card.coverage }}</span
        ><span v-if="metric === card.id" class="stat-selected">Graphing</span>
      </button>
    </div>
    <p class="dashboard-coverage">
      Totals cover {{ totals.covered }} of {{ totals.total }} segments.
      <span v-if="totals.covered < totals.total"
        >Unindexed or unreadable logs are excluded from distance and time.</span
      ><span v-else>Totals describe these recordings, not an odometer.</span>
    </p>
    <p v-if="demo" class="dashboard-coverage">
      The sample library has no decoded telemetry. Only recording counts are
      available.
    </p>
    <section
      class="dashboard-activity"
      aria-labelledby="driving-activity-title"
    >
      <header class="activity-controls">
        <h2 id="driving-activity-title">Driving activity</h2>
        <div
          v-glass-tabs
          class="segmented activity-group"
          role="group"
          aria-label="Group activity"
        >
          <button
            v-for="choice in [
              { id: 'drive', label: 'Drives' },
              { id: 'day', label: 'Days' },
              { id: 'month', label: 'Months' },
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
        {{ undated }} drives have no usable date and appear only in the list and
        totals for All recordings.
      </p>
    </section>
    <section class="dashboard-drives" aria-labelledby="dashboard-drives-title">
      <header class="activity-controls">
        <div>
          <h2 id="dashboard-drives-title">
            {{ chosen?.label || 'Drives in this period' }}
          </h2>
          <p role="status">
            {{ matching.length }} {{ matching.length === 1 ? 'drive' : 'drives'
            }}{{ chosen ? ' selected' : ''
            }}<span v-if="chosen">
              · {{ describeMetric(chosen.drives, metric, unit) }}</span
            >
          </p>
        </div>
        <button v-if="selected" class="text-button" @click="selected = ''">
          Clear selection
        </button>
      </header>
      <div v-if="!matching.length" class="dashboard-empty">
        <p>No recordings in this period.</p>
        <button v-if="period !== 'all'" class="button" @click="period = 'all'">
          Show all recordings
        </button>
      </div>
      <button
        v-for="item in visible"
        :key="item.drive.id"
        type="button"
        class="dashboard-drive"
        :aria-label="`Review ${driveDate(item.drive)} at ${driveTime(item.drive)}`"
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
            >{{ item.covered }}/{{ item.total }} segments indexed{{
              item.drive.dated ? '' : ' · file date'
            }}</small
          ></span
        ><span class="dashboard-drive-values"
          >{{ describeMetric([item], 'distance', unit)
          }}<small>{{ describeMetric([item], 'duration', unit) }}</small></span
        ><span class="dashboard-drive-arrow" aria-hidden="true">›</span>
      </button>
      <div v-if="matching.length > 20" class="dashboard-list-paging">
        <button class="button" :disabled="listPage === 0" @click="listPage--">
          Previous drives</button
        ><span
          >{{ listPage * 20 + 1 }}–{{
            Math.min(matching.length, (listPage + 1) * 20)
          }}
          of {{ matching.length }}</span
        ><button
          class="button"
          :disabled="(listPage + 1) * 20 >= matching.length"
          @click="listPage++"
        >
          Next drives
        </button>
      </div>
    </section>
    <details class="statistics-notes">
      <summary>How totals are measured</summary>
      <p>
        Only recordings still on this device are counted. Deleted footage is not
        lifetime driving history. GPS distance skips gaps over 10 seconds and
        implausible jumps. Recorded time is the observed log span, not engine-on
        time. Missing values stay unavailable; partial bars add only measured
        segments.
      </p>
      <p>
        Each drive is assigned to its local start date, including drives
        spanning midnight. Only dates with retained recordings are plotted.
        {{
          fileDates
            ? `${fileDates} drives use file modification dates because their folder names have no date.`
            : ''
        }}
        Dates that cannot be read are excluded from dated graphs.
      </p>
      <p>
        Statistics use cached index summaries. New metadata appears after parked
        indexing; opening this dashboard does not start a decoding job.
      </p>
    </details>
  </section>
</template>
