<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue';
import { Play, Pause, LoaderCircle } from 'lucide-vue-next';
import type { Entry } from '../library';
import { videoAddress } from '../indexer';
import RecordingPlayer from './RecordingPlayer.vue';
const props = defineProps<{ files: Entry[]; sample: boolean }>();
const host = ref<HTMLElement>();
const sources = ref<Record<string, string>>({}),
  errors = ref<Record<string, string>>({}),
  preparing = ref(false);
const cameras = computed(() =>
  props.files.filter((file) => /\.(hevc|ts|mp4|webm)$/.test(file.name)),
);
const selected = ref<string[]>([]);
const compatible = ref(false);
let controller: AbortController | undefined;
const owned: string[] = [];
function cleanup() {
  controller?.abort();
  for (const url of owned) URL.revokeObjectURL(url);
  owned.length = 0;
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
      const native = videoAddress(file, compatible.value);
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
function synchronize(play: boolean) {
  const videos = [...(host.value?.querySelectorAll('video') || [])];
  const time = videos[0]?.currentTime || 0;
  for (const video of videos) {
    if (video.readyState > 0) video.currentTime = time;
    if (play) video.play().catch(() => {});
    else video.pause();
  }
}
onBeforeUnmount(cleanup);
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
      <label class="checkbox-field"
        ><input v-model="compatible" type="checkbox" :disabled="preparing" />
        H.264 compatibility</label
      ><button
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
        :disabled="!Object.keys(sources).length"
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
        />
        <p v-else class="inline-error">{{ errors[file.url] }}</p>
      </div>
    </div>
    <p class="feed-hint">
      Feeds align to the first player when you press Play or Pause. This is
      segment-relative alignment, not sensor timestamp synchronization.
      dash-backend prepares cameras one at a time while parked and reuses cached
      previews.
    </p>
  </section>
</template>
