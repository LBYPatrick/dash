<script setup lang="ts">
import { ref } from 'vue';
import Sheet from './Sheet.vue';
import { Settings, Sun, Moon, Monitor } from '../icons';
import { theme, performanceMode, preferencesSaved } from '../appearance';
import { backendOrigin, indexStatus } from '../indexer';
import { vGlassTabs } from '../glassTabs';
defineProps<{ labeled?: boolean }>();
const emit = defineEmits<{ rebuild: [] }>();
const dialog = ref<InstanceType<typeof Sheet>>();
const themes = [
  { value: 'light', label: 'Light', icon: Sun },
  { value: 'dark', label: 'Dark', icon: Moon },
  { value: 'system', label: 'System', icon: Monitor },
] as const;
</script>
<template>
  <button
    :class="labeled ? 'sidebar-settings' : 'icon-button'"
    aria-label="Settings"
    aria-haspopup="dialog"
    @click="dialog?.showModal()"
  >
    <Settings :size="labeled ? 17 : 19" /><span v-if="labeled">Settings</span>
  </button>
  <Sheet
    ref="dialog"
    title="Settings"
    close-label="Close settings"
    class="settings-popup"
  >
    <div class="modal-inner">
      <fieldset class="appearance-settings">
        <legend>Appearance</legend>
        <div v-glass-tabs class="segmented theme-options">
          <label
            v-for="option in themes"
            :key="option.value"
            :class="{ selected: theme === option.value }"
          >
            <input
              v-model="theme"
              type="radio"
              name="theme"
              :value="option.value"
            />
            <component :is="option.icon" :size="18" />{{ option.label }}
          </label>
        </div>
      </fieldset>
      <label class="performance-setting">
        <span
          ><strong>Performance mode</strong
          ><small>Opaque surfaces, fewer visual effects.</small></span
        >
        <span class="settings-switch"
          ><input
            v-model="performanceMode"
            type="checkbox"
            role="switch"
            aria-label="Performance mode" /><span
            class="switch-track"
            aria-hidden="true"
          ></span
        ></span>
      </label>
      <p class="settings-note">Applies immediately. Saved on this browser.</p>
      <section v-if="backendOrigin" class="index-settings">
        <h3>Recordings</h3>
        <p>
          Refresh thumbnails and metadata. Original recordings stay untouched.
        </p>
        <button
          class="button full-width"
          type="button"
          :disabled="indexStatus.driving !== 'parked' || indexStatus.running"
          @click="emit('rebuild')"
        >
          {{ indexStatus.running ? 'Indexing…' : 'Rebuild index' }}
        </button>
        <p v-if="indexStatus.driving !== 'parked'" class="settings-note">
          Available when the vehicle is confirmed parked.
        </p>
        <p v-if="indexStatus.errors" class="settings-note" role="status">
          {{ indexStatus.errors }} files could not be indexed. Rebuild to retry.
        </p>
      </section>
      <p v-if="!preferencesSaved" role="status">
        Browser storage is unavailable. Preferences apply for this session.
      </p>
    </div>
  </Sheet>
</template>
