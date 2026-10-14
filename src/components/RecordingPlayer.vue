<script setup lang="ts">
import { formatNumber } from '../i18n';
import { useTranslation } from 'i18next-vue';
import {
  computed,
  useId,
  nextTick,
  ref,
  watch,
  onMounted,
  onBeforeUnmount,
} from 'vue';
import { Film, CircleAlert, Play, Pause, X } from '../icons';
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
const { t } = useTranslation();
const speedOptions = computed(() =>
  [0.25, 0.5, 0.75, 1, 1.25, 1.5, 2, 4].map((value) => ({
    value,
    label: `${formatNumber(value)}×`,
  })),
);
const props = withDefaults(
  defineProps<{
    source?: string;
    sample?: boolean;
    controls?: boolean;
    segments?: TimelineSegment[];
    controlsTarget?: HTMLElement;
  }>(),
  { controls: true },
);
const emit = defineEmits<{
  ended: [];
  timeupdate: [time: number, duration: number];
  positionframe: [time: number];
  loaded: [];
  fullscreenRequested: [];
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
const timestampErrorId = useId();
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
const hasEngagement = computed(() =>
  (props.segments || []).some((part) => !!part.metadata?.engagement?.length),
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
      error.value = t('player.playbackCouldNotStartRetryAfterThe');
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
    timestampError.value = t('player.timeRange', {
      end: formatTimestamp(duration.value, 3),
    });
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
type SafariVideo = HTMLVideoElement & { webkitEnterFullscreen?: () => void };
const fullscreenSupported = ref(
  !!document.fullscreenEnabled ||
    'webkitEnterFullscreen' in HTMLVideoElement.prototype,
);
const inFullscreen = ref(false);
function fullscreenChanged() {
  inFullscreen.value = document.fullscreenElement === root.value;
}
async function fullscreen() {
  if (!document.fullscreenElement) {
    emit('fullscreenRequested');
    await nextTick();
  }
  try {
    if (document.fullscreenElement) await document.exitFullscreen();
    else if (document.fullscreenEnabled && root.value?.requestFullscreen)
      await root.value.requestFullscreen();
    else if ((video.value as SafariVideo)?.webkitEnterFullscreen)
      (video.value as SafariVideo).webkitEnterFullscreen!();
    else throw new Error('Fullscreen unavailable');
  } catch {
    error.value = t('player.fullscreenIsUnavailableInThisBrowser');
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
    error.value = t('player.thisBrowserDoesNotSupportRecordingStreams');
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
    error.value = t(
      'player.playbackUnavailableTimelineIndexingAndCameraPreparation',
    );
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
  error.value = t('player.recordingUnavailableConfirmParkedStateAndRetry');
  if (playlist() && !stream && attempts++ < 10)
    retry = setTimeout(() => void initialize(), 2000);
}
onMounted(() => {
  document.addEventListener('fullscreenchange', fullscreenChanged);
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
  document.removeEventListener('fullscreenchange', fullscreenChanged);
  document.removeEventListener('visibilitychange', sync);
  motionPreference.removeEventListener('change', motionChanged);
  cleanup();
  resizeObserver?.disconnect();
});
</script>
<template>
  <div
    ref="root"
    class="player"
    :class="{ 'enhanced-player': source && controls }"
  >
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
      :aria-label="t('player.driveRecording')"
    />
    <div v-else class="player-placeholder">
      <Film :size="34" />
      <h3>{{ t('player.noRecordingAvailable') }}</h3>
      <p>{{ t('player.openOriginalFilesToDownloadAvailableFootage') }}</p>
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
        {{ t('player.retryPlayback') }}
      </button>
    </div>
    <Teleport
      :to="controlsTarget || 'body'"
      :disabled="!controlsTarget || inFullscreen"
    >
      <div v-if="source && controls" class="enhanced-controls">
        <div
          v-if="snapshots"
          class="snapshot-strip"
          :aria-label="t('player.roadRecordingSnapshots')"
        >
          <button
            v-for="frame in snapshotFrames"
            :key="frame.start"
            type="button"
            :aria-label="
              t('player.seekTo', { time: formatTimestamp(frame.start) })
            "
            @click="seek(frame.start)"
          >
            <img :src="frame.thumbnail" alt="" loading="lazy" /><span>{{
              formatTimestamp(frame.start)
            }}</span>
          </button>
          <span v-if="!snapshotFrames.length" class="muted">{{
            t('player.snapshotsAppearAfterParkedIndexing')
          }}</span>
        </div>
        <div
          v-if="engagement && hasEngagement"
          class="engagement-row"
          role="status"
        >
          <span
            class="status-dot"
            :class="{ amber: status !== t('player.engaged') }"
          ></span
          >{{ status }}
        </div>
        <svg
          v-if="engagement && hasEngagement && bands.length && duration"
          class="engagement-track"
          viewBox="0 0 1000 8"
          preserveAspectRatio="none"
          role="img"
          :aria-label="t('player.recordedEngagementTimeline')"
        >
          <title>
            {{ t('player.greenEngagedGrayEnabledButInactiveBlue') }}
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
          ><span class="sr-only">{{ t('player.recordingTimeline') }}</span
          ><input
            type="range"
            :aria-label="t('player.recordingTimeline')"
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
            :aria-label="
              paused ? t('player.playRecording') : t('player.pauseRecording')
            "
            :disabled="!duration"
            @click="togglePlay"
          >
            <Play v-if="paused" :size="16" /><Pause v-else :size="16" />
          </button>
          <button
            type="button"
            class="player-control"
            :aria-label="t('player.back10Seconds')"
            :disabled="!duration"
            @click="seek(time - 10)"
          >
            −10
          </button>
          <button
            type="button"
            class="player-control"
            :aria-label="t('player.forward10Seconds')"
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
              :aria-label="t('player.playbackTimestamp')"
              :aria-invalid="timestampError ? true : undefined"
              :aria-describedby="timestampError ? timestampErrorId : undefined"
              placeholder="m:ss.SSS"
              autocomplete="off"
              inputmode="text"
              enterkeyhint="go"
              autocapitalize="off"
              spellcheck="false"
            /><button type="submit" class="player-control">
              {{ t('player.go') }}
            </button>
            <button
              type="button"
              class="player-control"
              :aria-label="t('player.cancelTimestampEntry')"
              @click="closeTime"
            >
              <X :size="16" />
            </button>
          </form>
          <button
            v-else
            type="button"
            ref="timestampButton"
            class="timestamp-button player-control"
            :aria-label="t('player.enterPlaybackTimestamp')"
            :disabled="!duration"
            @click="editTime"
          >
            <span>{{ formatTimestamp(time, 3) }}</span
            ><span class="timestamp-total">
              / {{ formatTimestamp(duration, 3) }}</span
            >
          </button>
          <div class="playback-secondary">
            <slot name="camera" />
            <SelectMenu
              class="playback-speed"
              v-model="rate"
              :aria-label="t('player.playbackSpeed')"
              :options="speedOptions"
            />
            <button
              type="button"
              v-if="fullscreenSupported"
              class="player-control fullscreen-control"
              :aria-label="
                inFullscreen
                  ? t('player.exitFullscreenRecording')
                  : t('player.fullscreenRecording')
              "
              @click="fullscreen"
            >
              ⛶
            </button>
            <SessionActions :label="t('player.viewerOptions')"
              ><button
                type="button"
                :aria-pressed="snapshots"
                @click="snapshots = !snapshots"
              >
                {{
                  snapshots
                    ? t('player.hideSnapshots')
                    : t('player.showSnapshots')
                }}</button
              ><button
                type="button"
                :aria-pressed="engagement && hasEngagement"
                :disabled="!hasEngagement"
                :title="
                  hasEngagement
                    ? undefined
                    : t('player.noEngagementDataWasRecordedForThis')
                "
                @click="engagement = !engagement"
              >
                {{
                  hasEngagement
                    ? engagement
                      ? t('player.hideEngagement')
                      : t('player.showEngagement')
                    : t('player.engagementNotRecorded')
                }}
              </button></SessionActions
            >
          </div>
        </div>
        <p
          v-if="timestampError"
          :id="timestampErrorId"
          class="inline-error"
          role="alert"
        >
          {{ timestampError }}
        </p>
      </div>
    </Teleport>
    <span v-if="sample" class="sample-label">{{
      t('player.illustratedSample')
    }}</span>
  </div>
</template>
