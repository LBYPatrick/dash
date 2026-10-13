<script setup lang="ts">
import { ref, watch, onMounted, onBeforeUnmount } from 'vue';
import { Film, CircleAlert } from 'lucide-vue-next';
import type Hls from 'hls.js';
const props = withDefaults(
  defineProps<{ source?: string; sample?: boolean; controls?: boolean }>(),
  { controls: true },
);
const emit = defineEmits<{ ended: []; timeupdate: []; loaded: [] }>();
const video = ref<HTMLVideoElement>();
const error = ref('');
let stream: Hls | undefined;
let generation = 0;
let retry: ReturnType<typeof setTimeout> | undefined;
let attempts = 0;
const playlist = () => !!props.source?.includes('/api/playlist?');
function cleanup() {
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
onMounted(() => void initialize());
onBeforeUnmount(cleanup);
</script>
<template>
  <div class="player">
    <video
      v-if="source"
      ref="video"
      :src="playlist() ? undefined : source"
      :controls="controls"
      playsinline
      preload="metadata"
      :poster="sample ? '/sample-road.svg' : undefined"
      @ended="emit('ended')"
      @timeupdate="emit('timeupdate')"
      @loadedmetadata="emit('loaded')"
      @error="playbackError"
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
    <span v-if="sample" class="sample-label">ILLUSTRATED SAMPLE</span>
  </div>
</template>
