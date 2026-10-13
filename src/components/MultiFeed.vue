<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { Play, Pause, LoaderCircle } from 'lucide-vue-next';
import type { Entry } from '../library';
import { recordingAddress } from '../indexer';
import RecordingPlayer from './RecordingPlayer.vue';
const props = defineProps<{
  files: Entry[];
  sample: boolean;
  driveId: string;
}>();
const host = ref<HTMLElement>();
const sources = ref<Record<string, string>>({}),
  errors = ref<Record<string, string>>({}),
  preparing = ref(false);
const cameras = computed(() =>
  props.files.filter((file) => /\.(hevc|ts|mp4|webm)$/.test(file.name)),
);
const selected = ref<string[]>([]);
const time = ref(0),
  duration = ref(0),
  playing = ref(false),
  ready = ref(false);
let clock: ReturnType<typeof setInterval> | undefined;
let controller: AbortController | undefined;
function cleanup() {
  controller?.abort();
  sources.value = {};
}
watch(
  () => props.files,
  () => {
    cleanup();
    preparing.value = false;
    errors.value = {};
    selected.value = cameras.value
      .filter((file) => file.name !== 'qcamera.ts')
      .slice(0, 3)
      .map((file) => file.url);
    if (!selected.value.length && cameras.value[0])
      selected.value = [cameras.value[0].url];
  },
  { immediate: true },
);
async function prepare() {
  cleanup();
  errors.value = {};
  preparing.value = true;
  const run = new AbortController();
  controller = run;
  for (const file of cameras.value.filter((item) =>
    selected.value.includes(item.url),
  )) {
    if (run.signal.aborted) return;
    try {
      const native = sampleSource(file);
      if (native) sources.value[file.url] = native;
      else if (/\.(mp4|webm)$/.test(file.name))
        sources.value[file.url] = file.url;
      else errors.value[file.url] = 'dash-backend is unavailable.';
    } catch (error) {
      if (!run.signal.aborted)
        errors.value[file.url] = (error as Error).message;
    }
  }
  if (!run.signal.aborted) preparing.value = false;
}
function sampleSource(file: Entry) {
  return props.sample ? file.url : recordingAddress(props.driveId, file.name);
}
function videos() {
  return [...(host.value?.querySelectorAll('video') || [])];
}
function update() {
  const feeds = videos();
  ready.value =
    feeds.length > 0 && feeds.every((video) => video.readyState >= 2);
  const master = feeds[0];
  if (!master) return;
  time.value = master.currentTime;
  duration.value = Number.isFinite(master.duration) ? master.duration : 0;
  playing.value = !master.paused && !master.ended;
  if (playing.value)
    for (const follower of feeds.slice(1)) {
      if (
        follower.readyState > 0 &&
        Math.abs(follower.currentTime - master.currentTime) > 0.5 &&
        master.currentTime < follower.duration
      )
        follower.currentTime = master.currentTime;
    }
}
function seek(event: Event) {
  const target = Number((event.target as HTMLInputElement).value);
  for (const video of videos())
    if (video.readyState > 0)
      video.currentTime = Math.min(target, Math.max(0, video.duration - 0.01));
  update();
}
function synchronize(play: boolean) {
  const feeds = videos();
  const position = feeds[0]?.currentTime || 0;
  for (const video of feeds) {
    if (video.readyState > 0 && position < video.duration)
      video.currentTime = position;
    if (play) video.play().catch(() => {});
    else video.pause();
  }
  update();
}
function label(seconds: number) {
  return `${Math.floor(seconds / 60)}:${Math.floor(seconds % 60)
    .toString()
    .padStart(2, '0')}`;
}
onMounted(() => {
  clock = setInterval(update, 250);
});
onBeforeUnmount(() => {
  clearInterval(clock);
  cleanup();
});
</script>
<template>
  <section ref="host" class="multi-feed">
    <div class="feed-options">
      <label v-for="file in cameras" :key="file.url" class="checkbox-field"
        ><input
          v-model="selected"
          type="checkbox"
          :value="file.url"
          :disabled="preparing"
        />{{ file.name }}</label
      >
    </div>
    <div class="feed-actions">
      <button
        class="button"
        :disabled="preparing || !selected.length"
        @click="prepare"
      >
        <LoaderCircle v-if="preparing" class="spinning" :size="15" />{{
          preparing ? 'Preparing feeds…' : 'Load selected feeds'
        }}</button
      ><button
        class="icon-button"
        aria-label="Play feeds together"
        :disabled="!ready"
        @click="synchronize(true)"
      >
        <Play :size="18" /></button
      ><button
        class="icon-button"
        aria-label="Pause feeds together"
        :disabled="!Object.keys(sources).length"
        @click="synchronize(false)"
      >
        <Pause :size="18" />
      </button>
    </div>
    <div class="feed-grid">
      <div
        v-for="file in cameras.filter(
          (item) => sources[item.url] || errors[item.url],
        )"
        :key="file.url"
      >
        <div class="feed-label">{{ file.name }}</div>
        <RecordingPlayer
          v-if="sources[file.url]"
          :source="sources[file.url]"
          :sample="sample"
          :controls="false"
          @loaded="update"
          @timeupdate="update"
        />
        <p v-else class="inline-error">{{ errors[file.url] }}</p>
      </div>
    </div>
    <div class="shared-timeline">
      <span>{{ label(time) }}</span
      ><input
        aria-label="Recording timeline"
        type="range"
        min="0"
        :max="duration || 1"
        :value="time"
        step="0.1"
        :disabled="!duration"
        @input="seek"
      /><span>{{ label(duration) }}</span>
    </div>
  </section>
</template>
