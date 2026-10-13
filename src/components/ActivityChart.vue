<script setup lang="ts">
import {
  computed,
  nextTick,
  onBeforeUnmount,
  onMounted,
  ref,
  useId,
  watch,
} from 'vue';
import { scaleBand, scaleLinear } from 'd3-scale';
import {
  describeMetric,
  metricValue,
  metricUnit,
  type ActivityBucket,
  type ActivityMetric,
  type DistanceUnit,
} from '../statistics';
const props = defineProps<{
  buckets: ActivityBucket[];
  metric: ActivityMetric;
  unit: DistanceUnit;
}>();
const selected = defineModel<string>({ required: true });
const host = ref<HTMLDivElement>();
const width = ref(600),
  page = ref(0),
  inspected = ref(''),
  focused = ref('');
const pattern = useId();
let observer: ResizeObserver;
const pageSize = computed(() =>
  Math.max(4, Math.min(28, Math.floor((width.value - 68) / 36))),
);
const pages = computed(() =>
  Math.max(1, Math.ceil(props.buckets.length / pageSize.value)),
);
const end = computed(() =>
  Math.max(0, props.buckets.length - page.value * pageSize.value),
);
const start = computed(() => Math.max(0, end.value - pageSize.value));
const plotted = computed(() => props.buckets.slice(start.value, end.value));
const height = computed(() => (width.value < 500 ? 200 : 240));
const bottom = computed(() => height.value - 34);
const x = computed(() =>
  scaleBand<string>()
    .domain(plotted.value.map((b) => b.key))
    .range([44, width.value - 24])
    .padding(0.18),
);
const y = computed(() =>
  scaleLinear()
    .domain([
      0,
      Math.max(
        props.metric === 'drives' ? 1 : 0.000001,
        props.buckets.reduce(
          (m, b) =>
            Math.max(m, metricValue(b.drives, props.metric, props.unit).value),
          0,
        ) || 1,
      ),
    ])
    .nice()
    .range([bottom.value, 16]),
);
const ticks = computed(() =>
  y.value
    .ticks(4)
    .filter((n) => props.metric !== 'drives' || Number.isInteger(n)),
);
const bars = computed(() =>
  plotted.value.map((bucket) => ({
    bucket,
    ...metricValue(bucket.drives, props.metric, props.unit),
    x: x.value(bucket.key)!,
    width: x.value.bandwidth(),
    visualWidth: Math.min(56, x.value.bandwidth()),
    visualX: x.value(bucket.key)! + Math.max(0, (x.value.bandwidth() - 56) / 2),
  })),
);
const preview = computed(
  () =>
    props.buckets.find((b) => b.key === (inspected.value || selected.value)) ||
    plotted.value.at(-1),
);
const tabKey = computed(() =>
  plotted.value.some((b) => b.key === focused.value)
    ? focused.value
    : plotted.value.some((b) => b.key === selected.value)
      ? selected.value
      : plotted.value.at(-1)?.key,
);
const title = computed(
  () =>
    `${props.metric === 'distance' ? 'GPS distance' : props.metric === 'duration' ? 'Recorded time' : 'Drive count'} (${metricUnit(props.metric, props.unit)})`,
);
function describe(bucket: ActivityBucket) {
  const data = metricValue(bucket.drives, props.metric, props.unit);
  return `${bucket.label}: ${describeMetric(bucket.drives, props.metric, props.unit)} · ${bucket.drives.length} ${bucket.drives.length === 1 ? 'drive' : 'drives'}${props.metric === 'drives' ? '' : ` · ${data.observed}/${data.expected} segments with ${props.metric === 'distance' ? 'GPS distance' : 'recorded time'}`}`;
}
function toggle(key: string) {
  selected.value = selected.value === key ? '' : key;
}
async function changePage(amount: number) {
  page.value = Math.max(0, Math.min(pages.value - 1, page.value + amount));
  selected.value = '';
  inspected.value = '';
  focused.value = '';
}
async function key(event: KeyboardEvent, index: number) {
  const target =
    event.key === 'ArrowLeft'
      ? index - 1
      : event.key === 'ArrowRight'
        ? index + 1
        : event.key === 'Home'
          ? 0
          : event.key === 'End'
            ? bars.value.length - 1
            : undefined;
  if (event.key === 'Escape') {
    selected.value = '';
    inspected.value = focused.value;
    return;
  }
  if (target === undefined) return;
  event.preventDefault();
  await nextTick();
  host.value
    ?.querySelectorAll<HTMLButtonElement>('.activity-hit')
    [Math.max(0, Math.min(bars.value.length - 1, target))]?.focus();
}
watch(
  () => props.buckets.map((b) => b.key).join('\0'),
  () => {
    page.value = Math.min(page.value, pages.value - 1);
    if (selected.value && !props.buckets.some((b) => b.key === selected.value))
      selected.value = '';
  },
);
watch(pageSize, () => {
  const index = props.buckets.findIndex((b) => b.key === selected.value);
  page.value =
    index < 0
      ? 0
      : Math.floor((props.buckets.length - 1 - index) / pageSize.value);
  inspected.value = '';
});
onMounted(() => {
  observer = new ResizeObserver(
    ([entry]) => (width.value = Math.max(210, entry.contentRect.width)),
  );
  observer.observe(host.value!);
});
onBeforeUnmount(() => observer?.disconnect());
</script>
<template>
  <div ref="host" class="activity-visualization">
    <div class="chart-heading">
      <span>{{ title }}</span>
      <div v-if="pages > 1" class="chart-paging">
        <button
          class="icon-button"
          aria-label="Earlier activity"
          :disabled="page >= pages - 1"
          @click="changePage(1)"
        >
          ‹</button
        ><span>{{ start + 1 }}–{{ end }} / {{ buckets.length }}</span
        ><button
          class="icon-button"
          aria-label="Later activity"
          :disabled="page === 0"
          @click="changePage(-1)"
        >
          ›
        </button>
      </div>
    </div>
    <template v-if="buckets.length">
      <div
        class="activity-plot"
        :style="{ height: `${height}px` }"
        @pointerleave="inspected = ''"
      >
        <svg
          :viewBox="`0 0 ${width} ${height}`"
          aria-hidden="true"
          focusable="false"
        >
          <defs>
            <pattern
              :id="pattern"
              width="6"
              height="6"
              patternUnits="userSpaceOnUse"
            >
              <path d="M-1 1L1 -1M0 6L6 0M5 7L7 5" class="chart-hatching" />
            </pattern>
          </defs>
          <g v-for="tick in ticks" :key="tick">
            <line
              x1="44"
              :x2="width - 6"
              :y1="y(tick)"
              :y2="y(tick)"
              class="chart-grid"
            />
            <text x="36" :y="y(tick) + 4" text-anchor="end" class="chart-axis">
              {{ y.tickFormat(4)(tick) }}
            </text>
          </g>
          <g v-for="(bar, index) in bars" :key="bar.bucket.key">
            <rect
              v-if="bar.observed"
              :x="bar.visualX"
              :y="bar.value ? y(bar.value) : bottom - 2"
              :width="bar.visualWidth"
              :height="bar.value ? Math.max(2, bottom - y(bar.value)) : 2"
              rx="2"
              class="activity-bar"
              :class="{
                dimmed: selected && selected !== bar.bucket.key,
                inspected: preview?.key === bar.bucket.key,
              }"
            />
            <rect
              v-if="bar.observed && bar.observed < bar.expected"
              :x="bar.visualX"
              :y="y(bar.value)"
              :width="bar.visualWidth"
              :height="Math.max(2, bottom - y(bar.value))"
              :fill="`url(#${pattern})`"
            />
            <text
              v-if="!bar.observed"
              :x="bar.x + bar.width / 2"
              :y="bottom - 6"
              text-anchor="middle"
              class="chart-axis"
            >
              ?
            </text>
            <text
              v-if="
                index === 0 ||
                index === bars.length - 1 ||
                index % Math.max(1, Math.ceil(bars.length / 5)) === 0
              "
              :x="bar.x + bar.width / 2"
              :y="height - 10"
              text-anchor="middle"
              class="chart-axis"
            >
              {{ bar.bucket.shortLabel }}
            </text>
          </g>
        </svg>
        <div role="group" :aria-label="title">
          <button
            v-for="(bar, index) in bars"
            :key="bar.bucket.key"
            type="button"
            class="activity-hit"
            :style="{
              left: `${bar.x}px`,
              width: `${bar.width}px`,
              top: '12px',
              height: `${bottom - 10}px`,
            }"
            :aria-label="describe(bar.bucket)"
            :aria-pressed="selected === bar.bucket.key"
            :tabindex="tabKey === bar.bucket.key ? 0 : -1"
            @pointerenter="inspected = bar.bucket.key"
            @focus="
              inspected = bar.bucket.key;
              focused = bar.bucket.key;
            "
            @click="toggle(bar.bucket.key)"
            @keydown="key($event, index)"
          ></button>
        </div>
      </div>
      <p class="chart-readout">{{ preview ? describe(preview) : '' }}</p>
      <p class="chart-hint">
        Select a bar to find its drives. Hatched bars have partial metadata; ?
        means unavailable.
      </p>
      <details class="chart-table">
        <summary>View chart as table</summary>
        <table>
          <caption>
            {{
              title
            }}
            · visible activity
          </caption>
          <thead>
            <tr>
              <th scope="col">Period</th>
              <th scope="col">{{ metricUnit(metric, unit) }}</th>
              <th scope="col">Drives</th>
              <th scope="col"><span class="sr-only">Actions</span></th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="bucket in plotted" :key="bucket.key">
              <th scope="row">{{ bucket.label }}</th>
              <td>{{ describeMetric(bucket.drives, metric, unit) }}</td>
              <td>{{ bucket.drives.length }}</td>
              <td>
                <button
                  class="text-button"
                  :aria-pressed="selected === bucket.key"
                  @click="toggle(bucket.key)"
                >
                  {{ selected === bucket.key ? 'Clear' : 'Show drives' }}
                </button>
              </td>
            </tr>
          </tbody>
        </table>
      </details>
    </template>
    <p v-else class="dashboard-empty">
      No dated recordings in this period. Choose a wider period to explore your
      library.
    </p>
  </div>
</template>
