<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue';
import { Download, LoaderCircle } from '../icons';
import Sheet from './Sheet.vue';
import SelectMenu from './SelectMenu.vue';
import type { Drive, Entry } from '../library';
import { indexStatus } from '../indexer';
const props = defineProps<{
  drive: Drive;
  files: Entry[];
  sample: boolean;
}>();
const dialog = ref<InstanceType<typeof Sheet>>();
const format = ref<'zip' | 'mp4' | 'mcap' | 'raw'>('zip');
const camera = ref('qcamera.ts'),
  h264 = ref(false);
const status = ref(''),
  error = ref(''),
  busy = ref(false),
  download = ref('');
watch([format, camera, h264], () => {
  download.value = '';
  status.value = '';
  error.value = '';
});
let jobId = '';
let controller: AbortController | undefined;
const cameras = computed(() =>
  props.files.filter((file) => /\.(hevc|ts)$/.test(file.name)),
);
function open(selectedFormat?: 'zip' | 'mp4' | 'mcap' | 'raw') {
  if (selectedFormat) format.value = selectedFormat;
  camera.value =
    cameras.value.find((file) => file.name === 'qcamera.ts')?.name ||
    cameras.value[0]?.name ||
    '';
  error.value = '';
  status.value = '';
  download.value = '';
  dialog.value?.showModal();
}
function cancel() {
  controller?.abort();
  if (jobId && busy.value)
    void fetch(`/api/exports/${jobId}/cancel`, {
      method: 'POST',
      keepalive: true,
    }).catch(() => {});
  busy.value = false;
}
async function run() {
  busy.value = true;
  error.value = '';
  download.value = '';
  jobId = '';
  controller = new AbortController();
  const signal = controller.signal;
  try {
    const response = await fetch('/api/exports', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        drive: props.drive.id,
        format: format.value,
        camera: camera.value,
        h264: h264.value,
      }),
      signal,
    });
    let job = await response.json();
    if (!response.ok) throw new Error(job.error || 'Could not start export.');
    jobId = job.id;
    while (!signal.aborted) {
      status.value = job.message;
      if (job.status === 'complete') {
        download.value = `/api/exports/${job.id}/download`;
        return;
      }
      if (job.status === 'failed' || job.status === 'canceled')
        throw new Error(job.message);
      await new Promise<void>((resolve) => {
        const abort = () => {
          clearTimeout(timer);
          resolve();
        };
        const timer = setTimeout(() => {
          signal.removeEventListener('abort', abort);
          resolve();
        }, 1000);
        signal.addEventListener('abort', abort, { once: true });
      });
      signal.throwIfAborted();
      const progress = await fetch(`/api/exports/${jobId}`, { signal });
      if (!progress.ok)
        throw new Error('Export status is unavailable. Check the connection.');
      job = await progress.json();
    }
  } catch (e) {
    error.value = signal.aborted ? 'Export canceled.' : (e as Error).message;
  } finally {
    busy.value = false;
  }
}
onBeforeUnmount(cancel);
defineExpose({ open });
</script>
<template>
  <Sheet
    ref="dialog"
    title="Export recordings"
    close-label="Close export"
    @cancel="cancel"
    @close="cancel"
  >
    <div class="modal-inner">
      <p>
        All {{ drive.segments.length }} parts of this session are included. Your
        device prepares the download while parked.
      </p>
      <form @submit.prevent="run">
        <label class="field-label" for="export-format">Format</label
        ><SelectMenu
          id="export-format"
          v-model="format"
          aria-label="Format"
          :disabled="busy"
          :options="
            [
              { value: 'zip', label: 'ZIP · original cameras and logs' },
              { value: 'mp4', label: 'MP4 · one continuous camera recording' },
              {
                value: 'raw',
                label: 'Raw video · original camera parts (ZIP)',
              },
              {
                value: 'mcap',
                label: 'MCAP · Dash telemetry + original attachments',
              },
            ] as const
          "
        />
        <template v-if="format === 'mp4' || format === 'raw'"
          ><label class="field-label" for="export-camera">Camera</label
          ><SelectMenu
            id="export-camera"
            v-model="camera"
            aria-label="Export camera"
            :disabled="busy"
            :options="
              cameras.map((file) => ({ value: file.name, label: file.name }))
            "
          /><label v-if="format === 'mp4'" class="checkbox-field"
            ><input v-model="h264" type="checkbox" :disabled="busy" /> Convert
            to H.264 for wider compatibility (slower)</label
          ></template
        >
        <p class="export-limit">
          8 GiB per export. One processing job at a time. Temporary exports
          expire after one hour. Starting a drive stops active processing.
        </p>
        <p v-if="format === 'mcap'" class="export-limit">
          Uses dash.v1 JSON schemas. Original videos and logs are attachments;
          decoded GPS and vehicle samples are included when indexed.
        </p>
        <p v-if="sample" class="inline-error">
          Connect to dash-backend to export real recordings. Samples are for
          exploring the interface.
        </p>
        <p v-else-if="indexStatus.driving !== 'parked'" class="inline-error">
          Export is paused until the vehicle is confirmed parked.
        </p>
        <div v-if="error" class="inline-error" role="alert">{{ error }}</div>
        <p v-if="status" class="export-status" role="status">{{ status }}</p>
        <a
          v-if="download"
          class="button primary full-width"
          :href="download"
          download
          ><Download :size="16" />Download {{ format.toUpperCase() }}</a
        ><button
          v-else
          class="button primary full-width"
          :disabled="
            busy ||
            sample ||
            indexStatus.driving !== 'parked' ||
            ((format === 'mp4' || format === 'raw') && !camera)
          "
        >
          <LoaderCircle v-if="busy" class="spinning" :size="16" /><Download
            v-else
            :size="16"
          />{{ busy ? 'Preparing on device…' : 'Export recording' }}</button
        ><button
          v-if="busy"
          type="button"
          class="text-button centered"
          @click="cancel"
        >
          Cancel export
        </button>
      </form>
    </div>
  </Sheet>
</template>
