<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue';
import { Radio, Video, Square, Download } from 'lucide-vue-next';
import { indexStatus } from '../indexer';
import { saveBlob } from '../media';
const endpoint = '/api/stream';
let heartbeat: ReturnType<typeof setInterval> | undefined;
const camera = ref('road');
const status = ref('Ready when your device is.');
const busy = ref(false),
  connected = ref(false),
  recording = ref(false);
const video = ref<HTMLVideoElement>();
let peer: RTCPeerConnection | undefined;
let recorder: MediaRecorder | undefined;
let stream: MediaStream | undefined;
let request: AbortController | undefined;
let timeout: ReturnType<typeof setTimeout> | undefined;
const recordingBlob = ref<Blob>();
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
  status.value = 'Connecting to live camera…';
  const controller = new AbortController();
  request = controller;
  const pc = new RTCPeerConnection({ iceServers: [] });
  peer = pc;
  try {
    const url = new URL(endpoint, location.origin);
    if (!['http:', 'https:'].includes(url.protocol))
      throw new Error('Use an HTTP or HTTPS signaling address.');
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
        status.value = 'Live · direct local connection';
      }
      if (['failed', 'disconnected', 'closed'].includes(pc.connectionState)) {
        stop();
        status.value =
          'Stream disconnected. Reconnect when the device is ready.';
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
            status.value =
              'Live view stopped: vehicle is no longer confirmed parked.';
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
        answer.error || `Streaming service returned ${response.status}.`,
      );
    await pc.setRemoteDescription({ type: 'answer', sdp: answer.sdp });
    if (!connected.value)
      timeout = setTimeout(() => {
        stop();
        status.value =
          'The device answered, but no peer connection arrived. Check that both devices share a network and cameras are available.';
      }, 20000);
  } catch (error) {
    if (controller.signal.aborted) return;
    stop();
    status.value =
      (error as Error).message === 'Failed to fetch'
        ? 'Cannot reach the streaming service. Use the local bridge, check the device network, and ensure firmware supports webrtcd.'
        : (error as Error).message;
  }
}
function toggleRecording() {
  if (recording.value) {
    recorder?.stop();
    return;
  }
  if (!stream || !window.MediaRecorder) {
    status.value = 'This browser does not support recording incoming video.';
    return;
  }
  const mime = [
    'video/webm;codecs=vp9',
    'video/webm;codecs=vp8',
    'video/mp4',
  ].find((type) => MediaRecorder.isTypeSupported(type));
  if (!mime) {
    status.value =
      'No supported recording format is available in this browser.';
    return;
  }
  try {
    const parts: Blob[] = [];
    recordingBytes = 0;
    recordingBlob.value = undefined;
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
          status.value = 'Recording stopped at the 128 MB memory limit.';
        }
      }
    };
    recorder.onstop = () => {
      recording.value = false;
      recordingBlob.value = new Blob(parts, { type: mime });
    };
    recorder.onerror = () => {
      status.value = 'Recording failed. Try another browser.';
      recording.value = false;
    };
    recorder.start(1000);
    recording.value = true;
  } catch (error) {
    status.value = (error as Error).message;
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
      status.value =
        'Live view stopped: vehicle is no longer confirmed parked.';
    }
  },
);
onBeforeUnmount(() => {
  void stop();
});
</script>
<template>
  <section class="live-view">
    <div class="eyebrow"><Radio :size="14" /> LOCAL LIVE VIEW</div>
    <h1>Live cameras</h1>
    <p>
      Receive-only camera streaming over your local network. Firmware support is
      checked when you connect.
    </p>
    <div class="live-settings">
      <label
        >Camera<select
          v-model="camera"
          class="address-input"
          :disabled="busy || connected"
        >
          <option value="road">Road</option>
          <option value="wideRoad">Wide road</option>
          <option value="driver">Driver</option>
        </select></label
      ><button
        class="button primary"
        :disabled="indexStatus.driving !== 'parked'"
        @click="busy || connected ? stop() : start()"
      >
        {{ busy || connected ? 'Disconnect' : 'Start live view' }}
      </button>
    </div>
    <div class="live-player">
      <video
        ref="video"
        autoplay
        muted
        playsinline
        controls
        aria-label="Live device camera"
      />
      <div v-if="!connected" class="live-placeholder">
        <Radio :size="35" />
        <p>{{ status }}</p>
      </div>
    </div>
    <div class="live-actions">
      <span role="status">{{ connected ? status : 'No active stream' }}</span
      ><button class="button" :disabled="!connected" @click="toggleRecording">
        <Square v-if="recording" :size="15" /><Video v-else :size="16" />{{
          recording ? 'Stop recording' : 'Record this feed'
        }}</button
      ><button v-if="recordingBlob" class="button" @click="downloadRecording">
        <Download :size="16" />Save recording
      </button>
    </div>
    <div class="drive-note">
      <p>
        Keep this tab open while recording. Capture stops at approximately 128
        MB. Only firmware with managed offroad streaming is supported. Older
        unmanaged streaming services are intentionally rejected. Dash does not
        send driving controls.
      </p>
    </div>
  </section>
</template>
