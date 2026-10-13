<script setup lang="ts">
import { ref, watch } from 'vue';
import { Film, CircleAlert } from 'lucide-vue-next';
const props = defineProps<{ source?: string; sample?: boolean }>();
const emit = defineEmits<{ ended: [] }>();
const error = ref(false);
watch(
  () => props.source,
  () => (error.value = false),
);
</script>
<template>
  <div class="player">
    <video
      v-if="source"
      :key="source"
      :src="source"
      controls
      playsinline
      preload="metadata"
      :poster="sample ? '/sample-road.svg' : undefined"
      @ended="emit('ended')"
      @error="error = true"
      aria-label="Drive recording"
    />
    <div v-else class="player-placeholder">
      <Film :size="34" />
      <h3>No preview in this segment</h3>
      <p>Open Original files to download available recordings.</p>
    </div>
    <div v-if="error" class="player-error" role="alert">
      <CircleAlert :size="20" />Preview unavailable. Preparation requires parked
      state and ffmpeg. For HEVC, try H.264 compatibility in Multiple feeds.
    </div>
    <span v-if="sample" class="sample-label">ILLUSTRATED SAMPLE</span>
  </div>
</template>
