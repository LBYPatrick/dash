<script setup lang="ts">
import { backendMessage } from '../i18n/backend';
import { useTranslation } from 'i18next-vue';
import { computed, onBeforeUnmount, ref, watch } from 'vue';
import { Download, LoaderCircle } from '../icons';
import Sheet from './Sheet.vue';
import SelectMenu from './SelectMenu.vue';
import type { Drive, Entry } from '../library';
import { indexStatus } from '../indexer';
const { t } = useTranslation();
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
  if (busy.value) status.value = t('export.exportCanceled');
  controller?.abort();
  controller = undefined;
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
  const current = new AbortController();
  controller = current;
  const signal = current.signal;
  try {
    const response = await fetch('/api/exports', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        drive: props.drive.id,
        format: format.value,
        camera: camera.value,
        h264: h264.value,
        stream: true,
      }),
      signal,
    });
    let job = await response.json();
    signal.throwIfAborted();
    if (!response.ok)
      throw new Error(
        job.error ? backendMessage(job.error) : t('export.couldNotStartExport'),
      );
    jobId = job.id;
    const startDownload = () => {
      const link = document.createElement('a');
      link.href = `/api/exports/${job.id}/download`;
      link.download = job.name || '';
      document.body.append(link);
      link.click();
      link.remove();
    };
    if (job.streaming) startDownload();
    while (!signal.aborted) {
      status.value =
        job.status === 'queued'
          ? t('export.queued')
          : t('export.preparing', {
              percent: Math.round((job.progress || 0) * 100),
            });
      if (job.status === 'complete') {
        download.value = `/api/exports/${job.id}/download`;
        if (!job.streaming) startDownload();
        status.value = t('export.yourDownloadShouldStartAutomaticallyIfIt');
        return;
      }
      if (job.status === 'failed' || job.status === 'canceled')
        throw new Error(backendMessage(job.message));
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
        throw new Error(
          t('export.exportStatusIsUnavailableCheckTheConnection'),
        );
      job = await progress.json();
    }
  } catch (e) {
    if (controller === current)
      error.value = signal.aborted
        ? t('export.exportCanceled')
        : (e as Error).message;
  } finally {
    if (controller === current) busy.value = false;
  }
}
onBeforeUnmount(cancel);
defineExpose({ open });
</script>
<template>
  <Sheet
    ref="dialog"
    :title="t('export.exportRecordings')"
    :close-label="t('export.closeExport')"
    @cancel="cancel"
    @close="cancel"
  >
    <div class="modal-inner">
      <p>
        {{ t('export.allParts', { count: drive.segments.length }) }}
      </p>
      <form @submit.prevent="run">
        <label class="field-label" for="export-format">{{
          t('export.format')
        }}</label
        ><SelectMenu
          id="export-format"
          v-model="format"
          :aria-label="t('export.format')"
          :disabled="busy"
          :options="
            [
              { value: 'zip', label: t('export.zipOriginalCamerasAndLogs') },
              {
                value: 'mp4',
                label: t('export.mp4OneContinuousCameraRecording'),
              },
              {
                value: 'raw',
                label: t('export.rawVideoOriginalCameraPartsZip'),
              },
              {
                value: 'mcap',
                label: t('export.mcapVideoTelemetryAndOriginals'),
              },
            ] as const
          "
        />
        <template v-if="format === 'mp4' || format === 'raw'"
          ><label class="field-label" for="export-camera">{{
            t('app.camera')
          }}</label
          ><SelectMenu
            id="export-camera"
            v-model="camera"
            :aria-label="t('export.exportCamera')"
            :disabled="busy"
            :options="
              cameras.map((file) => ({ value: file.name, label: file.name }))
            "
          /><label v-if="format === 'mp4'" class="checkbox-field"
            ><input v-model="h264" type="checkbox" :disabled="busy" />
            {{ t('export.convertToH264ForWiderCompatibility') }}</label
          ></template
        >
        <p class="export-limit">
          {{ t('export.8GibPerExportOneProcessingJob') }}
        </p>
        <p v-if="format === 'mcap'" class="export-limit">
          {{ t('export.includesFoxgloveVideoChannelsDashTelemetryWhen') }}
        </p>
        <p v-if="sample" class="inline-error">
          {{ t('export.connectToDashBackendToExportReal') }}
        </p>
        <p v-else-if="indexStatus.driving !== 'parked'" class="inline-error">
          {{ t('export.exportIsPausedUntilTheVehicleIs') }}
        </p>
        <div v-if="error" class="inline-error" role="alert">{{ error }}</div>
        <p v-if="status" class="export-status" role="status">{{ status }}</p>
        <Transition
          name="task-feedback"
          mode="out-in"
          @before-leave="(element) => element.setAttribute('inert', '')"
          @leave-cancelled="(element) => element.removeAttribute('inert')"
        >
          <a
            v-if="download"
            key="download"
            class="button primary full-width"
            :href="download"
            download
            @click.prevent="run"
            ><Download :size="16" />{{ t('export.downloadAgain') }}</a
          ><button
            v-else
            key="prepare"
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
            />{{
              busy
                ? t('diagnostics.preparingDownload')
                : t('export.downloadRecording')
            }}
          </button>
        </Transition>
        <button
          v-if="busy"
          type="button"
          class="text-button centered"
          @click="cancel"
        >
          {{ t('export.cancelExport') }}
        </button>
      </form>
    </div>
  </Sheet>
</template>
