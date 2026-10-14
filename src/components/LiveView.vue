<script setup lang="ts">
import { backendMessage } from '../i18n/backend';
import { useTranslation } from 'i18next-vue';
import {
  computed,
  shallowRef,
  onBeforeUnmount,
  onMounted,
  ref,
  watch,
} from 'vue';
import { Radio, Video, Square, Download } from '../icons';
import SelectMenu from './SelectMenu.vue';
import { indexStatus } from '../indexer';
import { saveBlob } from '../media';
import {
  retainedLiveCapture as recordingBlob,
  beginLiveCapture,
  finishLiveCapture,
} from '../liveCapture';
const { t } = useTranslation();
const endpoint = '/api/stream';
let heartbeat: ReturnType<typeof setInterval> | undefined;
const camera = ref('road');
const statusMessage = shallowRef<() => string>(() =>
  t('live.readyWhenYourDeviceIs'),
);
const status = computed(() => backendMessage(statusMessage.value()));
const busy = ref(false),
  connected = ref(false),
  recording = ref(false);
const video = ref<HTMLVideoElement>();
let peer: RTCPeerConnection | undefined;
let recorder: MediaRecorder | undefined;
let stream: MediaStream | undefined;
let request: AbortController | undefined;
let timeout: ReturnType<typeof setTimeout> | undefined;
let recordingBytes = 0;
async function stop() {
  const hadPeer = !!peer;
  clearInterval(heartbeat);
  heartbeat = undefined;
  request?.abort();
  clearTimeout(timeout);
  timeout = undefined;
  if (recorder?.state === 'recording') recorder.stop();
  peer?.close();
  peer = undefined;
  stream?.getTracks().forEach((track) => track.stop());
  stream = undefined;
  if (video.value) video.value.srcObject = null;
  connected.value = false;
  busy.value = false;
  if (hadPeer)
    await fetch('/api/stream/stop', { method: 'POST', keepalive: true }).catch(
      () => {},
    );
}
async function start() {
  await stop();
  busy.value = true;
  statusMessage.value = () => t('live.connectingToLiveCamera');
  const controller = new AbortController();
  request = controller;
  const pc = new RTCPeerConnection({ iceServers: [] });
  peer = pc;
  try {
    const url = new URL(endpoint, location.origin);
    if (!['http:', 'https:'].includes(url.protocol))
      throw new Error(t('live.useAnHttpOrHttpsSignalingAddress'));
    pc.addTransceiver('video', { direction: 'recvonly' });
    pc.createDataChannel('data');
    pc.ontrack = (event) => {
      if (controller.signal.aborted) return;
      stream = new MediaStream([event.track]);
      if (video.value) {
        video.value.srcObject = stream;
        video.value.play().catch(() => {});
      }
    };
    pc.onconnectionstatechange = () => {
      if (controller.signal.aborted) return;
      if (pc.connectionState === 'connected') {
        clearTimeout(timeout);
        busy.value = false;
        connected.value = true;
        statusMessage.value = () => t('live.liveDirectLocalConnection');
      }
      if (['failed', 'disconnected', 'closed'].includes(pc.connectionState)) {
        stop();
        statusMessage.value = () =>
          t('live.streamDisconnectedReconnectWhenTheDeviceIs');
      }
    };
    await pc.setLocalDescription(await pc.createOffer());
    if (pc.iceGatheringState !== 'complete')
      await new Promise<void>((resolve) => {
        const timer = setTimeout(resolve, 2500);
        pc.addEventListener('icegatheringstatechange', () => {
          if (pc.iceGatheringState === 'complete') {
            clearTimeout(timer);
            resolve();
          }
        });
      });
    controller.signal.throwIfAborted();
    heartbeat = setInterval(() => {
      void fetch('/api/stream/heartbeat', { method: 'POST' })
        .then((response) => response.json())
        .then((data) => {
          if (data.driving !== 'parked') {
            void stop();
            statusMessage.value = () =>
              t('live.liveViewStoppedVehicleIsNoLonger');
          }
        })
        .catch(() => {
          void stop();
        });
    }, 3000);
    const response = await fetch(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        sdp: pc.localDescription?.sdp,
        cameras: [camera.value],
        bridge_services_in: [],
        bridge_services_out: [],
      }),
      signal: AbortSignal.any([controller.signal, AbortSignal.timeout(45000)]),
    });
    const answer = await response.json();
    if (!response.ok || answer.error)
      throw new Error(
        answer.error
          ? answer.error
          : t('live.httpError', { status: response.status }),
      );
    await pc.setRemoteDescription({ type: 'answer', sdp: answer.sdp });
    if (!connected.value)
      timeout = setTimeout(() => {
        stop();
        statusMessage.value = () =>
          t('live.theDeviceAnsweredButNoPeerConnection');
      }, 20000);
  } catch (error) {
    if (controller.signal.aborted) return;
    stop();
    statusMessage.value = () =>
      (error as Error).message === 'Failed to fetch'
        ? t('live.cannotReachTheStreamingServiceUseThe')
        : (error as Error).message;
  }
}
function toggleRecording() {
  if (recording.value) {
    recorder?.stop();
    return;
  }
  if (!stream || !window.MediaRecorder) {
    statusMessage.value = () =>
      t('live.thisBrowserDoesNotSupportRecordingIncoming');
    return;
  }
  const mime = [
    'video/webm;codecs=vp9',
    'video/webm;codecs=vp8',
    'video/mp4',
  ].find((type) => MediaRecorder.isTypeSupported(type));
  if (!mime) {
    statusMessage.value = () =>
      t('live.noSupportedRecordingFormatIsAvailableIn');
    return;
  }
  try {
    const parts: Blob[] = [];
    let captureRun = 0;
    recordingBytes = 0;
    recorder = new MediaRecorder(stream, { mimeType: mime });
    recorder.ondataavailable = (event) => {
      if (event.data.size) {
        parts.push(event.data);
        recordingBytes += event.data.size;
        if (
          recordingBytes > 128 * 1024 * 1024 &&
          recorder?.state === 'recording'
        ) {
          recorder.stop();
          statusMessage.value = () =>
            t('live.recordingStoppedAtThe128MbMemory');
        }
      }
    };
    recorder.onstop = () => {
      recording.value = false;
      finishLiveCapture(captureRun, new Blob(parts, { type: mime }));
    };
    recorder.onerror = () => {
      statusMessage.value = () => t('live.recordingFailedTryAnotherBrowser');
      recording.value = false;
    };
    recorder.start(1000);
    captureRun = beginLiveCapture();
    recording.value = true;
  } catch (error) {
    statusMessage.value = () => (error as Error).message;
  }
}
function downloadRecording() {
  if (recordingBlob.value)
    saveBlob(
      recordingBlob.value,
      `dash-live-${Date.now()}.${recordingBlob.value.type.includes('mp4') ? 'mp4' : 'webm'}`,
    );
}
watch(
  () => indexStatus.value.driving,
  (state) => {
    if (state !== 'parked' && peer) {
      void stop();
      statusMessage.value = () => t('live.liveViewStoppedVehicleIsNoLonger');
    }
  },
);
function backgrounded() {
  if (document.hidden && (connected.value || busy.value || recording.value)) {
    void stop();
    statusMessage.value = () => t('live.liveViewStoppedWhenDashMovedTo');
  }
}
onMounted(() => document.addEventListener('visibilitychange', backgrounded));
onBeforeUnmount(() => {
  document.removeEventListener('visibilitychange', backgrounded);
  void stop();
});
</script>
<template>
  <section class="live-view">
    <div class="eyebrow">
      <Radio :size="14" /> {{ t('live.localLiveView') }}
    </div>
    <h1>{{ t('app.liveCameras') }}</h1>
    <p>
      {{ t('live.receiveOnlyCameraStreamingOverYourLocal') }}
    </p>
    <div class="live-settings">
      <label
        >{{ t('app.camera')
        }}<SelectMenu
          v-model="camera"
          :aria-label="t('live.liveCamera')"
          :disabled="busy || connected"
          :options="[
            { value: 'road', label: t('live.road') },
            { value: 'wideRoad', label: t('live.wideRoad') },
            { value: 'driver', label: t('live.driver') },
          ]" /></label
      ><button
        class="button primary"
        :disabled="indexStatus.driving !== 'parked'"
        @click="busy || connected ? stop() : start()"
      >
        {{ busy || connected ? t('live.disconnect') : t('live.startLiveView') }}
      </button>
    </div>
    <p
      v-if="indexStatus.driving !== 'parked' && !connected"
      class="settings-note"
      role="status"
    >
      {{ t('live.liveViewIsAvailableWhenTheVehicle') }}
    </p>
    <div class="live-player">
      <video
        ref="video"
        autoplay
        muted
        playsinline
        controls
        :aria-label="t('live.liveDeviceCamera')"
      />
      <Transition name="task-feedback">
        <div v-if="!connected" class="live-placeholder">
          <Radio :size="35" />
          <p>{{ status }}</p>
        </div>
      </Transition>
    </div>
    <div class="live-actions">
      <span role="status">{{ status }}</span
      ><button class="button" :disabled="!connected" @click="toggleRecording">
        <Square v-if="recording" :size="15" /><Video v-else :size="16" />{{
          recording
            ? t('live.stopRecording')
            : recordingBlob
              ? t('live.recordNewClip')
              : t('live.recordThisFeed')
        }}</button
      ><button v-if="recordingBlob" class="button" @click="downloadRecording">
        <Download :size="16" />{{ t('live.saveRecording') }}
      </button>
    </div>
    <p v-if="recordingBlob && !recording" class="capture-note" role="status">
      {{ t('live.yourClipStaysAvailableWhenSwitchingDash') }}
    </p>
    <div class="drive-note">
      <p>
        {{ t('live.keepThisTabOpenWhileRecordingCapture') }}
      </p>
    </div>
  </section>
</template>
