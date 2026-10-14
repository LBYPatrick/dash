import { shallowRef } from 'vue';
// Keep one bounded finalized capture across view/component lifetimes. LiveView
// still stops the peer and recorder immediately when leaving; no background
// recording, persistent storage, or new device processing is introduced.
export const retainedLiveCapture = shallowRef<Blob>();
let generation = 0;
export function beginLiveCapture() {
  // Called only after MediaRecorder starts successfully. Explicitly recording
  // a new clip replaces the old one rather than retaining two large captures.
  retainedLiveCapture.value = undefined;
  return ++generation;
}
export function finishLiveCapture(run: number, blob: Blob) {
  // A late stop event from a departed view cannot overwrite a newer capture.
  if (run === generation && blob.size) retainedLiveCapture.value = blob;
}
