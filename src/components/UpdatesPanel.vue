<script setup lang="ts">
import { onMounted, onBeforeUnmount, ref, computed } from 'vue';
import SelectMenu from './SelectMenu.vue';
import { indexStatus } from '../indexer';
import { Download, RefreshCw, LoaderCircle } from '../icons';
interface Build {
  version: string;
  channel: string;
  revision: string;
  tag: string;
}
interface UpdateState {
  repository: string;
  pendingHealth?: boolean;
  managed: boolean;
  phase: string;
  current: Build;
  channel: string;
  automatic: boolean;
  release?: Build;
  transferred?: number;
  total?: number;
  message?: string;
}
const state = ref<UpdateState>();
const error = ref('');
const channel = ref('main');
const automatic = ref(true);
const submitting = ref(false);
const busy = computed(
  () =>
    submitting.value ||
    ['checking', 'downloading', 'verifying', 'restarting'].includes(
      state.value?.phase || '',
    ),
);
const parked = computed(() => indexStatus.value.driving === 'parked');
const progress = computed(() =>
  state.value?.total
    ? Math.min(
        100,
        Math.round(((state.value.transferred || 0) / state.value.total) * 100),
      )
    : 0,
);
const restarting = ref('');
let stopped = false,
  timer: ReturnType<typeof setTimeout> | undefined;
const controller = new AbortController();
async function load() {
  try {
    const response = await fetch('/api/updates', {
      signal: controller.signal,
      cache: 'no-store',
    });
    if (!response.ok) throw new Error('Update information is unavailable.');
    const current: UpdateState = await response.json();
    state.value = current;
    if (!submitting.value) {
      channel.value = current.channel;
      automatic.value = current.automatic;
    }
    if (
      restarting.value &&
      !current.pendingHealth &&
      current.current.tag !== restarting.value
    ) {
      // Refresh the installed PWA as well as the page after the backend restarts.
      try {
        const registrations = await navigator.serviceWorker?.getRegistrations();
        await Promise.all(
          (registrations || []).map((r) => r.update().catch(() => {})),
        );
      } catch {
        /* Insecure LAN origins have no service worker. */
      }
      location.reload();
    } else if (
      restarting.value &&
      current.current.tag === restarting.value &&
      current.phase !== 'restarting'
    ) {
      restarting.value = '';
    }
  } catch (e) {
    if (!controller.signal.aborted && !restarting.value)
      error.value = (e as Error).message;
  }
  if (!stopped) timer = setTimeout(load, 2000);
}
async function action(action: string) {
  if (busy.value || !parked.value) return;
  submitting.value = true;
  error.value = '';
  try {
    const response = await fetch('/api/updates', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        action,
        channel: channel.value,
        automatic: automatic.value,
      }),
      signal: controller.signal,
    });
    const result = await response.json();
    if (!response.ok) throw new Error(result.error || 'Could not update Dash.');
    state.value = result;
    if (action === 'install') restarting.value = result.current.tag;
  } catch (e) {
    if (!controller.signal.aborted) error.value = (e as Error).message;
    if (state.value) {
      channel.value = state.value.channel;
      automatic.value = state.value.automatic;
    }
  } finally {
    submitting.value = false;
  }
}
onMounted(load);
onBeforeUnmount(() => {
  stopped = true;
  controller.abort();
  clearTimeout(timer);
});
</script>
<template>
  <section class="update-settings" aria-label="Dash updates">
    <h3>Updates</h3>
    <template v-if="state">
      <p class="settings-note">
        {{ state.current.version
        }}<span v-if="state.current.channel !== 'local'">
          · {{ state.current.channel }}</span
        >
      </p>
      <template v-if="state.managed">
        <label class="field-label" for="update-channel">Delivery channel</label>
        <SelectMenu
          id="update-channel"
          v-model="channel"
          aria-label="Delivery channel"
          :disabled="busy || !parked"
          :options="[
            { value: 'main', label: 'Main · stable' },
            { value: 'nightly', label: 'Nightly' },
            { value: 'dev', label: 'Dev' },
          ]"
          @update:model-value="action('configure')"
        />
        <label class="performance-setting update-automatic">
          <span
            ><strong>Check automatically</strong
            ><small
              >Looks for builds while parked. Downloads start when you
              choose.</small
            ></span
          >
          <span class="settings-switch"
            ><input
              v-model="automatic"
              type="checkbox"
              role="switch"
              aria-label="Check for updates automatically"
              :disabled="busy || !parked"
              @change="action('configure')" /><span
              class="switch-track"
              aria-hidden="true"
            ></span
          ></span>
        </label>
        <p v-if="state.release" class="update-release">
          {{ state.release.version }}
          <a
            :href="`https://github.com/${state.repository}/releases/tag/${encodeURIComponent(state.release.tag)}`"
            target="_blank"
            rel="noopener noreferrer"
            >Release details</a
          >
        </p>
        <p v-if="!parked" class="settings-note">
          Updates are available while parked.
        </p>
        <p v-if="state.phase === 'current'" class="settings-note" role="status">
          This channel is up to date.
        </p>
        <p v-if="state.message" class="settings-note" role="status">
          {{ state.message }}
        </p>
        <p v-if="error" class="inline-error" role="alert">{{ error }}</p>
        <Transition name="task-feedback">
          <div
            v-if="state.phase === 'downloading' && state.total"
            class="update-progress"
          >
            <progress
              :value="progress"
              max="100"
              aria-label="Update download progress"
            ></progress
            ><span>{{ progress }}%</span>
          </div>
        </Transition>
        <button
          class="button full-width"
          :class="{
            primary: state.phase === 'available' || state.phase === 'ready',
          }"
          :disabled="busy || !parked"
          @click="
            action(
              state.phase === 'available'
                ? 'download'
                : state.phase === 'ready'
                  ? 'install'
                  : 'check',
            )
          "
        >
          <LoaderCircle v-if="busy" class="spinning" :size="16" /><Download
            v-else-if="state.phase === 'available'"
            :size="16"
          /><RefreshCw v-else :size="16" />
          {{
            state.phase === 'restarting'
              ? 'Reconnecting…'
              : state.phase === 'checking'
                ? 'Checking…'
                : state.phase === 'downloading'
                  ? 'Downloading…'
                  : state.phase === 'verifying'
                    ? 'Verifying…'
                    : state.phase === 'available'
                      ? 'Download update'
                      : state.phase === 'ready'
                        ? 'Restart Dash and install'
                        : 'Check for updates'
          }}
        </button>
      </template>
      <p v-else class="settings-note">
        {{
          state.message || 'Source build. Install a release to enable updates.'
        }}
      </p>
    </template>
    <p v-else-if="error" class="settings-note">{{ error }}</p>
    <p v-else class="settings-note">Loading update information…</p>
  </section>
</template>
