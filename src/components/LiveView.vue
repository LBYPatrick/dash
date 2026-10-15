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
import {
  Radio,
  Video,
  Square,
  Download,
  CircleHelp,
  LoaderCircle,
} from '../icons';
import Sheet from './Sheet.vue';
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
let viewerLease = '';
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
const details = ref<InstanceType<typeof Sheet>>();
const recordingSeconds = ref(0);
const recordingTime = computed(
  () =>
    `${Math.floor(recordingSeconds.value / 60)}:${String(recordingSeconds.value % 60).padStart(2, '0')}`,
);
let recordingTimer: ReturnType<typeof setInterval> | undefined;
let peer: RTCPeerConnection | undefined;
let recorder: MediaRecorder | undefined;
let stream: MediaStream | undefined;
let request: AbortController | undefined;
let timeout: ReturnType<typeof setTimeout> | undefined;
let recordingBytes = 0;
async function stop() {
  const hadPeer = !!peer;
  const lease = viewerLease;
  viewerLease = '';
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
    await fetch('/api/stream/stop', {
      method: 'POST',
      keepalive: true,
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ lease }),
    }).catch(() => {});
}
async function start() {
  await stop();
  busy.value = true;
  statusMessage.value = () => t('live.connectingToLiveCamera');
  const lease = Array.from(
    crypto.getRandomValues(new Uint8Array(16)),
    (value) => value.toString(16).padStart(2, '0'),
  ).join('');
  viewerLease = lease;
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
      void fetch('/api/stream/heartbeat', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ lease }),
      })
        .then((response) => response.json())
        .then((data) => {
          if (controller.signal.aborted || viewerLease !== lease) return;
          if (data.driving !== 'parked' || (connected.value && !data.active)) {
            void stop();
            statusMessage.value = () =>
              t('live.liveViewStoppedVehicleIsNoLonger');
          }
        })
        .catch(() => {
          if (!controller.signal.aborted && viewerLease === lease) void stop();
        });
    }, 3000);
    const response = await fetch(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        lease,
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
      clearInterval(recordingTimer);
      recordingTimer = undefined;
      recording.value = false;
      finishLiveCapture(captureRun, new Blob(parts, { type: mime }));
    };
    recorder.onerror = () => {
      clearInterval(recordingTimer);
      recordingTimer = undefined;
      statusMessage.value = () => t('live.recordingFailedTryAnotherBrowser');
      recording.value = false;
    };
    recorder.start(1000);
    captureRun = beginLiveCapture();
    recording.value = true;
    recordingSeconds.value = 0;
    const began = Date.now();
    recordingTimer = setInterval(() => {
      recordingSeconds.value = Math.floor((Date.now() - began) / 1000);
    }, 1000);
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
function pageLeft() {
  void stop();
}
onMounted(() => {
  document.addEventListener('visibilitychange', backgrounded);
  window.addEventListener('pagehide', pageLeft);
});
onBeforeUnmount(() => {
  document.removeEventListener('visibilitychange', backgrounded);
  window.removeEventListener('pagehide', pageLeft);
  void stop();
  clearInterval(recordingTimer);
});
</script>
<template>
  <section
    class="live-view"
    :aria-label="t('app.liveCameras')"
    :aria-busy="busy"
  >
    <header class="live-heading">
      <h1>{{ t('app.live') }}</h1>
      <span
        class="live-state"
        :class="{ 'is-connected': connected, 'is-recording': recording }"
      >
        <LoaderCircle v-if="busy" :size="14" class="spinning" />
        <span v-else class="live-state-dot" aria-hidden="true"></span>
        {{
          recording
            ? t('live.recording')
            : busy
              ? t('live.connecting')
              : connected
                ? t('live.connected')
                : t('live.standby')
        }}
        <time v-if="recording">{{ recordingTime }}</time>
      </span>
      <button
        class="icon-button live-info"
        :aria-label="t('live.aboutLiveView')"
        @click="details?.showModal()"
      >
        <CircleHelp :size="19" />
      </button>
    </header>
    <div class="live-player">
      <video
        ref="video"
        autoplay
        muted
        playsinline
        controls
        :aria-label="t('live.liveDeviceCamera')"
      />
      <Transition name="live-feedback">
        <div
          v-if="!connected"
          class="live-placeholder"
          role="status"
          aria-live="polite"
        >
          <LoaderCircle v-if="busy" :size="28" class="spinning" />
          <Radio v-else :size="28" />
          <p>
            {{
              indexStatus.driving !== 'parked' && !busy
                ? t('live.liveViewIsAvailableWhenTheVehicle')
                : status
            }}
          </p>
        </div>
      </Transition>
      <span
        v-if="connected"
        :class="
          status === t('live.liveDirectLocalConnection')
            ? 'sr-only'
            : 'live-notice'
        "
        role="status"
        >{{ status }}</span
      >
    </div>
    <div class="live-controls">
      <SelectMenu
        v-model="camera"
        :aria-label="t('live.liveCamera')"
        :disabled="busy || connected"
        :options="[
          { value: 'road', label: t('live.road') },
          { value: 'wideRoad', label: t('live.wideRoad') },
          { value: 'driver', label: t('live.driver') },
        ]"
      />
      <button
        class="button primary live-connect"
        :disabled="!busy && !connected && indexStatus.driving !== 'parked'"
        @click="busy || connected ? stop() : start()"
      >
        {{ busy || connected ? t('live.disconnect') : t('live.startLiveView') }}
      </button>
      <button
        class="button live-record"
        :class="{ 'is-recording': recording }"
        :disabled="!connected"
        :aria-pressed="recording"
        @click="toggleRecording"
      >
        <Square v-if="recording" :size="15" /><Video v-else :size="16" />
        {{
          recording
            ? t('live.stopRecording')
            : recordingBlob
              ? t('live.recordNewClip')
              : t('live.recordThisFeed')
        }}
      </button>
      <button
        v-if="recordingBlob"
        class="button live-save"
        @click="downloadRecording"
      >
        <Download :size="16" />{{ t('live.saveRecording') }}
      </button>
    </div>
    <Sheet
      ref="details"
      :title="t('live.aboutLiveView')"
      :close-label="t('app.closeHelp')"
    >
      <div class="live-information">
        <p>{{ t('live.receiveOnlyCameraStreamingOverYourLocal') }}</p>
        <p>{{ t('live.keepThisTabOpenWhileRecordingCapture') }}</p>
        <p>{{ t('live.yourClipStaysAvailableWhenSwitchingDash') }}</p>
      </div>
    </Sheet>
  </section>
</template>

<style scoped>
/* The viewport belongs to the camera. Help lives in the same Shelf sheet used
   elsewhere rather than competing with the picture and its controls. */
:global(#app .workspace > .live-detail),
:global(#app .workspace.show-detail > .live-detail) {
  display: flex;
  flex-direction: column;
  min-height: 0;
  overflow: hidden;
  padding: 12px 20px max(12px, env(safe-area-inset-bottom));
}
:global(#app .live-detail > .offline-banner),
:global(#app .live-detail > .demo-banner) {
  flex-shrink: 0;
}
.live-view {
  display: flex;
  flex: 1;
  flex-direction: column;
  min-width: 0;
  min-height: 0;
  gap: 12px;
}
.live-heading {
  display: flex;
  flex: 0 0 auto;
  align-items: center;
  gap: 14px;
  min-height: 36px;
}
:global(#app .live-view .live-heading h1) {
  margin: 0;
  color: var(--ink);
  font-size: 21px;
  font-weight: 600;
  letter-spacing: -0.02em;
}
.live-state {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  color: var(--muted);
  font-size: 12px;
  font-weight: 500;
  min-width: 0;
}
.live-state-dot {
  width: 6px;
  height: 6px;
  flex-shrink: 0;
  border-radius: 50%;
  background: currentColor;
}
.live-state.is-connected {
  color: var(--color-primary-text);
}
.live-state.is-recording {
  color: var(--danger-text);
}
.live-state time {
  font-variant-numeric: tabular-nums;
}
:global(#app .live-view .live-info) {
  margin-left: auto;
  flex-shrink: 0;
}
:global(#app .live-view .live-player) {
  flex: 1;
  min-height: 72px;
  aspect-ratio: auto;
  border-radius: 0;
  background: #0b1014;
}
.live-player video {
  object-fit: contain;
}
.live-notice {
  position: absolute;
  bottom: 56px;
  left: 12px;
  right: 12px;
  width: fit-content;
  max-width: calc(100% - 24px);
  margin: auto;
  padding: 10px 14px;
  color: #fff;
  background: #24313d;
  border-radius: var(--radius-control);
  font-size: 13px;
  line-height: 1.4;
}
:global(#app .live-view .live-placeholder) {
  color: #ecf1f5;
  gap: 12px;
  padding: 16px 24px;
  background: #0b1014;
}
:global(#app .live-view .live-placeholder p) {
  margin: 0;
  max-width: 480px;
  font-size: 14px;
  line-height: 1.5;
  color: #ecf1f5;
  overflow-wrap: anywhere;
}
.live-controls {
  display: flex;
  flex: 0 0 auto;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  min-width: 0;
}
:global(#app .live-view .live-controls .selectmenu) {
  width: min(200px, 25%);
}
:global(#app .live-view .live-controls .select-trigger) {
  width: 100%;
  min-height: 44px;
}
:global(#app .live-view .live-controls .button) {
  min-height: 44px;
  padding: 8px 14px;
  font-size: 13px;
  white-space: normal;
  text-align: center;
  line-height: 1.25;
}
.live-record {
  margin-left: auto;
}
:global(#app .live-view .live-record.is-recording) {
  background: var(--danger-fill);
  color: var(--danger-text);
}
.live-information {
  display: grid;
  gap: 16px;
}
.live-information p {
  margin: 0;
  color: var(--ink);
  font-size: 14px;
  line-height: 1.6;
}
.live-feedback-enter-active {
  transition: opacity 180ms var(--ease-out);
}
.live-feedback-leave-active {
  transition: opacity 140ms var(--ease-out);
}
.live-feedback-enter-from,
.live-feedback-leave-to {
  opacity: 0;
}
@media (max-width: 900px) {
  /* The phone's app bar already names this page. Keep its semantic heading
     without drawing the same title twice. */
  .live-heading h1 {
    position: absolute;
    width: 1px;
    height: 1px;
    overflow: hidden;
    clip-path: inset(50%);
    white-space: nowrap;
  }
}
@media (max-width: 600px) and (min-height: 501px) {
  :global(#app .workspace.show-detail > .live-detail) {
    padding: 8px 12px max(12px, env(safe-area-inset-bottom));
  }
  .live-view {
    gap: 8px;
  }
  .live-controls {
    display: grid;
    grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
  }
  :global(#app .live-view .live-controls .selectmenu) {
    width: 100%;
  }
  .live-record {
    margin-left: 0;
  }
  .live-record:last-child {
    grid-column: 1 / -1;
  }
}
@media (max-height: 500px) {
  :global(#app .workspace.show-detail > .live-detail) {
    padding: 4px 12px max(6px, env(safe-area-inset-bottom));
  }
  .live-view {
    gap: 4px;
  }
  .live-heading {
    min-height: 28px;
  }
  .live-controls {
    flex-wrap: nowrap;
  }
  :global(#app .live-view .live-controls .selectmenu) {
    flex: 1;
    width: 0;
    min-width: 95px;
  }
  :global(#app .live-view .live-controls .button) {
    padding-inline: 10px;
  }
  .live-record {
    margin-left: 0;
  }
}
/* Large text and unusually short windows keep every control reachable. Normal
   phone/desktop dimensions fit without this fallback scrolling. */
@media (max-height: 280px) {
  :global(#app .workspace.show-detail > .live-detail) {
    overflow-y: auto;
  }
  .live-view {
    flex: 0 0 auto;
  }
  :global(#app .live-view .live-player) {
    flex: none;
    height: 120px;
  }
}
@media (prefers-reduced-motion: reduce) {
  .live-feedback-enter-active,
  .live-feedback-leave-active {
    transition-duration: 80ms;
  }
  .spinning {
    animation: none;
  }
}
</style>
