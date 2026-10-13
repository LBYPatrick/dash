<script setup lang="ts">
import { ref, onBeforeUnmount } from 'vue';
import { Download, LoaderCircle } from '../icons';
const props = defineProps<{ available: boolean }>();
const busy = ref(false);
const error = ref('');
let controller: AbortController | undefined;
onBeforeUnmount(() => controller?.abort());
async function download() {
  if (busy.value || !props.available) return;
  busy.value = true;
  error.value = '';
  controller = new AbortController();
  const timer = setTimeout(() => controller?.abort(), 15000);
  try {
    const response = await fetch('/api/diagnostics/download', {
      signal: controller.signal,
    });
    if (!response.ok)
      throw new Error(
        'Could not download diagnostics. Check the backend connection and try again.',
      );
    if (!response.headers.get('Content-Type')?.startsWith('text/plain'))
      throw new Error('The backend returned an unexpected diagnostic file.');
    const blob = await response.blob();
    if (blob.size > 2 * 1024 * 1024)
      throw new Error('The diagnostic file exceeds the download limit.');
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement('a');
    anchor.href = url;
    anchor.download = `dash-diagnostics-${new Date().toISOString().replace(/[:.]/g, '-')}.txt`;
    document.body.appendChild(anchor);
    anchor.click();
    anchor.remove();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  } catch (cause) {
    error.value =
      (cause as Error).name === 'AbortError'
        ? 'Download timed out. Reconnect and try again.'
        : (cause as Error).message;
  } finally {
    clearTimeout(timer);
    busy.value = false;
  }
}
</script>
<template>
  <section class="diagnostics-section">
    <h3>Share a debug report</h3>
    <p>
      Download recent backend logs and runtime status. No recordings or GPS
      samples are included. Logs may contain recording names; review the file
      before sharing.
    </p>
    <button
      class="button full-width"
      :disabled="!available || busy"
      @click="download"
    >
      <LoaderCircle v-if="busy" :size="16" class="spinning" /><Download
        v-else
        :size="16"
      />
      {{ busy ? 'Preparing download…' : 'Download backend diagnostics' }}
    </button>
    <p v-if="!available" class="muted">
      Connect to dash-backend to download diagnostics.
    </p>
    <p v-if="error" class="inline-error" role="alert">{{ error }}</p>
  </section>
</template>
<style scoped>
.diagnostics-section {
  border-top: 1px solid var(--border, #e0e5de);
  margin-top: 24px;
  padding-top: 8px;
}
</style>
