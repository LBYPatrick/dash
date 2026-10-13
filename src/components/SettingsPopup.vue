<script setup lang="ts">
import { ref } from 'vue';
import Sheet from './Sheet.vue';
import { Settings, Sun, Moon, Monitor } from '../icons';
import { theme, performanceMode, preferencesSaved } from '../appearance';
import { vGlassTabs } from '../glassTabs';
const dialog = ref<InstanceType<typeof Sheet>>();
const themes = [
  { value: 'light', label: 'Light', icon: Sun },
  { value: 'dark', label: 'Dark', icon: Moon },
  { value: 'system', label: 'System', icon: Monitor },
] as const;
</script>
<template>
  <button
    class="icon-button"
    aria-label="Settings"
    aria-haspopup="dialog"
    @click="dialog?.showModal()"
  >
    <Settings :size="19" />
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
      <p v-if="!preferencesSaved" role="status">
        Browser storage is unavailable. Preferences apply for this session.
      </p>
    </div>
  </Sheet>
</template>
