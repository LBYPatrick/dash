<script setup lang="ts">
import {
  computed,
  nextTick,
  ref,
  watch,
  onMounted,
  onBeforeUnmount,
} from 'vue';
import { Film, CircleAlert, Play, Pause } from '../icons';
import type Hls from 'hls.js';
import SelectMenu from './SelectMenu.vue';
import SessionActions from './SessionActions.vue';
import { readStored, writeStored } from '../storage';
import { performanceMode } from '../appearance';
import {
  formatTimestamp,
  parseTimestamp,
  engagementAt,
  engagementBands,
  type TimelineSegment,
} from '../viewerTimeline';
const props = withDefaults(
  defineProps<{
    source?: string;
    sample?: boolean;
    controls?: boolean;
    segments?: TimelineSegment[];
  }>(),
  { controls: true },
);
const emit = defineEmits<{
  ended: [];
  timeupdate: [time: number, duration: number];
  positionframe: [time: number];
  loaded: [];
}>();
const video = ref<HTMLVideoElement>();
const error = ref('');
const time = ref(0),
  duration = ref(0),
  paused = ref(true),
  rate = ref(1),
  editingTime = ref(false),
  timestamp = ref(''),
  timestampError = ref('');
const timestampInput = ref<HTMLInputElement>();
const timestampButton = ref<HTMLButtonElement>();
const options = readStored<{ snapshots?: boolean; engagement?: boolean }>(
  'dash.viewerOptions',
  {},
);
const snapshots = ref(!!options.snapshots),
  engagement = ref(!!options.engagement);
watch([snapshots, engagement], () =>
  writeStored('dash.viewerOptions', {
    snapshots: snapshots.value,
    engagement: engagement.value,
  }),
);
const bands = computed(() => engagementBands(props.segments || []));
const telemetryTime = ref(0);
const status = computed(() =>
  engagementAt(props.segments || [], telemetryTime.value),
);
const motionPreference = window.matchMedia('(prefers-reduced-motion: reduce)');
const reducedMotion = ref(motionPreference.matches);
let clockFrame = 0;
let lastPositionFrame = -Infinity;
function stopClock() {
  cancelAnimationFrame(clockFrame);
  clockFrame = 0;
}
function clockAllowed() {
  const element = video.value;
  return (
    props.controls &&
    element &&
    !element.paused &&
    !element.ended &&
    !element.seeking &&
    element.readyState >= 3 &&
    !document.hidden &&
    !performanceMode.value &&
    !reducedMotion.value
  );
}
function updateClock() {
  if (!clockAllowed()) {
    stopClock();
    return;
  }
  if (clockFrame) return;
  clockFrame = requestAnimationFrame((now) => {
    clockFrame = 0;
    // Read the actual media clock: never advance while buffering or ease a seek.
    if (!clockAllowed()) return;
    time.value = video.value!.currentTime;
    // The existing media clock also drives GPS interpolation, capped at 30 Hz.
    // Engagement lookup remains on native timeupdate; never decode telemetry here.
    if (now - lastPositionFrame >= 1000 / 30) {
      lastPositionFrame = now;
      emit('positionframe', time.value);
    }
    updateClock();
  });
}
function motionChanged(event: MediaQueryListEvent) {
  reducedMotion.value = event.matches;
}
watch([performanceMode, reducedMotion, () => props.controls], updateClock);
const frameLimit = ref(8);
const root = ref<HTMLDivElement>();
let resizeObserver: ResizeObserver;
const snapshotFrames = computed(() => {
  const parts = (props.segments || []).filter(
    (part) => part.thumbnail && part.start < duration.value,
  );
  // At most 16 cached frames are rendered; no video decoding or backend job.
  const step = Math.max(1, Math.ceil(parts.length / frameLimit.value));
  return parts
    .filter((_, index) => index % step === 0)
    .slice(0, frameLimit.value);
});
function sync() {
  if (!video.value) return;
  time.value = video.value.currentTime;
  duration.value = Number.isFinite(video.value.duration)
    ? video.value.duration
    : 0;
  paused.value = video.value.paused;
  telemetryTime.value = time.value;
  emit('timeupdate', time.value, duration.value);
  updateClock();
}
function seek(value: number) {
  if (video.value && duration.value > 0) {
    video.value.currentTime = Math.max(0, Math.min(duration.value, value));
    sync();
  }
}
function seekSlider(event: Event) {
  seek(Number((event.target as HTMLInputElement).value));
}
async function togglePlay() {
  if (!video.value) return;
  if (video.value.paused) {
    try {
      await video.value.play();
    } catch {
      error.value =
        'Playback could not start. Retry after the recording loads.';
    }
  } else video.value.pause();
  sync();
}
async function editTime() {
  timestamp.value = formatTimestamp(time.value, 3);
  timestampError.value = '';
  editingTime.value = true;
  await nextTick();
  timestampInput.value?.focus();
  timestampInput.value?.select();
}
async function closeTime() {
  editingTime.value = false;
  timestampError.value = '';
  await nextTick();
  timestampButton.value?.focus();
}
function submitTime() {
  const seconds = parseTimestamp(timestamp.value);
  if (seconds === undefined || seconds > duration.value + 0.0005) {
    timestampError.value = `Enter a time from 0:00 to ${formatTimestamp(duration.value, 3)}.`;
    return;
  }
  seek(seconds);
  void closeTime();
}
watch(rate, (value) => {
  if (video.value) video.value.playbackRate = value;
});
function loaded() {
  if (video.value) video.value.playbackRate = rate.value;
  sync();
  emit('loaded');
}
async function fullscreen() {
  try {
    await video.value?.parentElement?.requestFullscreen();
  } catch {
    error.value = 'Fullscreen is unavailable in this browser.';
  }
}
let stream: Hls | undefined;
let generation = 0;
let retry: ReturnType<typeof setTimeout> | undefined;
let attempts = 0;
const playlist = () => !!props.source?.includes('/api/playlist?');
function cleanup() {
  stopClock();
  generation++;
  clearTimeout(retry);
  stream?.destroy();
  stream = undefined;
}
async function initialize() {
  const position = video.value?.currentTime || 0;
  const resume = !!video.value && !video.value.paused;
  cleanup();
  error.value = '';
  const run = generation;
  const source = props.source;
  const element = video.value;
  if (!source || !element || !playlist()) return;
  element.addEventListener(
    'loadedmetadata',
    () => {
      if (run !== generation) return;
      if (position > 0 && Number.isFinite(element.duration))
        element.currentTime = Math.min(
          position,
          Math.max(0, element.duration - 0.05),
        );
      if (resume) void element.play().catch(() => {});
    },
    { once: true },
  );
  if (element.canPlayType('application/vnd.apple.mpegurl')) {
    element.src = source;
    return;
  }
  const { default: Hls } = await import('hls.js');
  if (run !== generation) return;
  if (!Hls.isSupported()) {
    error.value = 'This browser does not support recording streams.';
    return;
  }
  stream = new Hls({
    startPosition: position,
    fragLoadPolicy: {
      default: {
        ...Hls.DefaultConfig.fragLoadPolicy.default,
        maxTimeToFirstByteMs: 185000,
        maxLoadTimeMs: 210000,
      },
    },
    maxBufferLength: 15,
    maxMaxBufferLength: 60,
    backBufferLength: 15,
    maxBufferSize: 32 * 1024 * 1024,
  });
  stream.on(Hls.Events.ERROR, (_, data) => {
    if (!data.fatal || run !== generation) return;
    error.value =
      'Playback unavailable. Timeline indexing and camera preparation require parked state. Retry after indexing completes.';
    if (data.type === Hls.ErrorTypes.NETWORK_ERROR && attempts++ < 10)
      retry = setTimeout(() => void initialize(), 2000);
  });
  stream.on(Hls.Events.MANIFEST_PARSED, () => {
    error.value = '';
  });
  stream.loadSource(source);
  stream.attachMedia(element);
}
watch(
  () => props.source,
  () => {
    attempts = 0;
    void initialize();
  },
  { flush: 'post' },
);
function playbackError() {
  error.value = 'Recording unavailable. Confirm parked state and retry.';
  if (playlist() && !stream && attempts++ < 10)
    retry = setTimeout(() => void initialize(), 2000);
}
onMounted(() => {
  document.addEventListener('visibilitychange', sync);
  motionPreference.addEventListener('change', motionChanged);
  void initialize();
  resizeObserver = new ResizeObserver(
    () =>
      (frameLimit.value = Math.max(
        1,
        Math.min(16, Math.floor((root.value?.clientWidth || 640) / 80)),
      )),
  );
  if (root.value) resizeObserver.observe(root.value);
});
onBeforeUnmount(() => {
  document.removeEventListener('visibilitychange', sync);
  motionPreference.removeEventListener('change', motionChanged);
  cleanup();
  resizeObserver?.disconnect();
});
</script>
<template>
  <div ref="root" class="player">
    <video
      v-if="source"
      ref="video"
      :src="playlist() ? undefined : source"
      :controls="false"
      playsinline
      preload="metadata"
      :poster="sample ? '/sample-road.svg' : undefined"
      @ended="
        sync();
        emit('ended');
      "
      @timeupdate="sync"
      @play="sync"
      @playing="sync"
      @waiting="stopClock"
      @seeking="stopClock"
      @pause="sync"
      @seeked="sync"
      @durationchange="sync"
      @loadedmetadata="loaded"
      @error="playbackError"
      tabindex="0"
      @click="togglePlay"
      @keydown.space.prevent="togglePlay"
      @keydown.left.prevent="seek(time - 5)"
      @keydown.right.prevent="seek(time + 5)"
      aria-label="Drive recording"
    />
    <div v-else class="player-placeholder">
      <Film :size="34" />
      <h3>No recording available</h3>
      <p>Open Original files to download available footage.</p>
    </div>
    <div v-if="error" class="player-error" role="alert">
      <CircleAlert :size="20" /><span>{{ error }}</span
      ><button
        class="button"
        @click="
          attempts = 0;
          initialize();
        "
      >
        Retry playback
      </button>
    </div>
    <div v-if="source && controls" class="enhanced-controls">
      <div
        v-if="snapshots"
        class="snapshot-strip"
        aria-label="Road recording snapshots"
      >
        <button
          v-for="frame in snapshotFrames"
          :key="frame.start"
          type="button"
          :aria-label="`Seek to ${formatTimestamp(frame.start)}`"
          @click="seek(frame.start)"
        >
          <img :src="frame.thumbnail" alt="" loading="lazy" /><span>{{
            formatTimestamp(frame.start)
          }}</span>
        </button>
        <span v-if="!snapshotFrames.length" class="muted"
          >Snapshots appear after parked indexing.</span
        >
      </div>
      <div v-if="engagement" class="engagement-row" role="status">
        <span class="status-dot" :class="{ amber: status !== 'Engaged' }"></span
        >{{ status }}
      </div>
      <svg
        v-if="engagement && bands.length && duration"
        class="engagement-track"
        viewBox="0 0 1000 8"
        preserveAspectRatio="none"
        role="img"
        aria-label="Recorded engagement timeline"
      >
        <title>
          Green: engaged. Gray: enabled but inactive. Blue: disengaged. Blank:
          no recorded state.
        </title>
        <rect
          v-for="(band, index) in bands"
          :key="index"
          :x="(band.start / duration) * 1000"
          :width="
            (Math.max(0, Math.min(duration, band.end) - band.start) /
              duration) *
            1000
          "
          height="8"
          :class="`engagement-${band.state}`"
        />
      </svg>
      <label class="video-timeline"
        ><span class="sr-only">Recording timeline</span
        ><input
          type="range"
          aria-label="Recording timeline"
          min="0"
          :max="duration || 1"
          step="0.001"
          :aria-valuetext="formatTimestamp(time, 3)"
          :value="time"
          :disabled="!duration"
          @input="seekSlider"
      /></label>
      <div class="playback-toolbar">
        <button
          type="button"
          class="player-control"
          :aria-label="paused ? 'Play recording' : 'Pause recording'"
          :disabled="!duration"
          @click="togglePlay"
        >
          <Play v-if="paused" :size="16" /><Pause v-else :size="16" />
        </button>
        <button
          type="button"
          class="player-control"
          aria-label="Back 10 seconds"
          :disabled="!duration"
          @click="seek(time - 10)"
        >
          −10
        </button>
        <button
          type="button"
          class="player-control"
          aria-label="Forward 10 seconds"
          :disabled="!duration"
          @click="seek(time + 10)"
        >
          +10
        </button>
        <form
          v-if="editingTime"
          class="timestamp-form"
          @submit.prevent="submitTime"
          @keydown.esc.prevent.stop="closeTime"
        >
          <input
            ref="timestampInput"
            v-model="timestamp"
            aria-label="Playback timestamp"
            placeholder="m:ss.SSS"
            autocomplete="off"
          /><button type="submit" class="player-control">Go</button>
        </form>
        <button
          v-else
          type="button"
          ref="timestampButton"
          class="timestamp-button player-control"
          aria-label="Enter playback timestamp"
          :disabled="!duration"
          @click="editTime"
        >
          <span>{{ formatTimestamp(time, 3) }}</span
          ><span class="timestamp-total">
            / {{ formatTimestamp(duration, 3) }}</span
          >
        </button>
        <SelectMenu
          v-model="rate"
          aria-label="Playback speed"
          :options="
            [0.25, 0.5, 0.75, 1, 1.25, 1.5, 2, 4].map((value) => ({
              value,
              label: `${value}×`,
            }))
          "
        />
        <button
          type="button"
          class="player-control fullscreen-control"
          aria-label="Fullscreen recording"
          @click="fullscreen"
        >
          ⛶
        </button>
        <SessionActions label="Viewer options"
          ><button
            type="button"
            :aria-pressed="snapshots"
            @click="snapshots = !snapshots"
          >
            {{ snapshots ? 'Hide' : 'Show' }} snapshots</button
          ><button
            type="button"
            :aria-pressed="engagement"
            @click="engagement = !engagement"
          >
            {{ engagement ? 'Hide' : 'Show' }} engagement status
          </button></SessionActions
        >
      </div>
      <p v-if="timestampError" class="inline-error" role="alert">
        {{ timestampError }}
      </p>
    </div>
    <span v-if="sample" class="sample-label">ILLUSTRATED SAMPLE</span>
  </div>
</template>
