<script setup lang="ts">
import { backendMessage } from '../i18n/backend';
import { useTranslation } from 'i18next-vue';
import { onMounted, onBeforeUnmount, ref, computed, watch } from 'vue';
import SelectMenu from './SelectMenu.vue';
import { indexStatus } from '../indexer';
import { Download, RefreshCw, LoaderCircle } from '../icons';
import {
  updateState as state,
  updateError as error,
  updateSubmitting as submitting,
  updateBusy as busy,
  refreshUpdates,
  observeUpdates,
  updateAction,
} from '../updates';
const { t } = useTranslation();
const channel = ref('main');
const automatic = ref(true);
const parked = computed(() => indexStatus.value.driving === 'parked');
const progress = computed(() =>
  state.value?.total
    ? Math.min(
        100,
        Math.round(((state.value.transferred || 0) / state.value.total) * 100),
      )
    : 0,
);
watch(
  state,
  (current) => {
    if (current && !submitting.value) {
      channel.value = current.channel;
      automatic.value = current.automatic;
    }
  },
  { immediate: true },
);
async function action(action: string) {
  await updateAction(action, channel.value, automatic.value);
  if (state.value) {
    channel.value = state.value.channel;
    automatic.value = state.value.automatic;
  }
}
let stopObserving: (() => void) | undefined;
onMounted(() => {
  stopObserving = observeUpdates();
});
onBeforeUnmount(() => stopObserving?.());
</script>
<template>
  <section class="update-settings" :aria-label="t('updates.dashUpdates')">
    <template v-if="state">
      <p class="settings-note">
        {{ state.current.version
        }}<span v-if="state.current.channel !== 'local'">
          · {{ state.current.channel }}</span
        >
      </p>
      <template v-if="state.managed">
        <label class="field-label" for="update-channel">{{
          t('updates.deliveryChannel')
        }}</label>
        <SelectMenu
          id="update-channel"
          v-model="channel"
          :aria-label="t('updates.deliveryChannel')"
          :disabled="busy || !parked"
          :options="[
            { value: 'main', label: t('updates.mainStable') },
            { value: 'nightly', label: t('updates.nightly') },
            { value: 'dev', label: t('updates.dev') },
          ]"
          @update:model-value="action('configure')"
        />
        <label class="performance-setting update-automatic">
          <span
            ><strong>{{ t('updates.checkAutomatically') }}</strong
            ><small>{{
              t('updates.looksForBuildsWhileParkedDownloadsStart')
            }}</small></span
          >
          <span class="settings-switch"
            ><input
              v-model="automatic"
              type="checkbox"
              role="switch"
              :aria-label="t('updates.checkForUpdatesAutomatically')"
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
            >{{ t('updates.releaseDetails') }}</a
          >
        </p>
        <p v-if="!parked" class="settings-note">
          {{ t('updates.updatesAreAvailableWhileParked') }}
        </p>
        <p v-if="state.phase === 'current'" class="settings-note" role="status">
          {{ t('updates.thisChannelIsUpToDate') }}
        </p>
        <p v-if="state.message" class="settings-note" role="status">
          {{
            state.pendingHealth
              ? t('updates.startupCheck')
              : state.phase === 'ready'
                ? t('updates.ready')
                : state.phase === 'error'
                  ? t('updates.failed')
                  : state.phase === 'restarting'
                    ? t('updates.reconnecting')
                    : state.phase === 'downloading'
                      ? t('updates.downloading')
                      : state.phase === 'verifying'
                        ? t('updates.verifying')
                        : backendMessage(state.message)
          }}
        </p>
        <details
          v-if="state.phase === 'error' && state.message"
          class="settings-note"
        >
          <summary>{{ t('common.technicalDetails') }}</summary>
          <p>{{ state.message }}</p>
        </details>
        <p v-if="error" class="inline-error" role="alert">{{ error }}</p>
        <Transition name="task-feedback">
          <div
            v-if="state.phase === 'downloading' && state.total"
            class="update-progress"
          >
            <progress
              :value="progress"
              max="100"
              :aria-label="t('updates.updateDownloadProgress')"
            ></progress
            ><span>{{ progress }}%</span>
          </div>
        </Transition>
        <button
          type="button"
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
              ? t('updates.reconnecting')
              : state.phase === 'checking'
                ? t('updates.checking')
                : state.phase === 'downloading'
                  ? t('updates.downloading')
                  : state.phase === 'verifying'
                    ? t('updates.verifying')
                    : state.phase === 'available'
                      ? t('updates.downloadUpdate')
                      : state.phase === 'ready'
                        ? t('updates.restartDashAndInstall')
                        : t('updates.checkForUpdates')
          }}
        </button>
      </template>
      <p v-else class="settings-note">
        {{ t('updates.sourceBuildInstallAReleaseToEnable') }}
      </p>
    </template>
    <template v-else-if="error">
      <p class="inline-error" role="alert">{{ error }}</p>
      <button class="button full-width" type="button" @click="refreshUpdates">
        <RefreshCw :size="16" />{{ t('updates.checkForUpdates') }}
      </button>
    </template>
    <p v-else class="settings-note">
      {{ t('updates.loadingUpdateInformation') }}
    </p>
  </section>
</template>

<style scoped>
.update-settings {
  margin-top: 0 !important;
}
.settings-note {
  overflow-wrap: anywhere;
}
</style>
