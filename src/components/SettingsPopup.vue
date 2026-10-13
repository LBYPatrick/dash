<script setup lang="ts">
import { ref } from 'vue';
import { Settings, Sun, Moon, Monitor, X } from 'lucide-vue-next';
import { theme, performanceMode, preferencesSaved } from '../appearance';
import { vGlassTabs } from '../glassTabs';
const dialog = ref<HTMLDialogElement>();
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
  <dialog
    ref="dialog"
    class="modal settings-popup"
    aria-labelledby="settings-title"
  >
    <div class="modal-inner">
      <button
        class="icon-button modal-close"
        aria-label="Close settings"
        @click="dialog?.close()"
      >
        <X :size="20" />
      </button>
      <h2 id="settings-title">Settings</h2>
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
          ><small>Opaque surfaces, without glass or blur.</small></span
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
  </dialog>
</template>
