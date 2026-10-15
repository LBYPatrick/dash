<script setup lang="ts">
import { useTranslation } from 'i18next-vue';
import { ref, computed } from 'vue';
import SelectMenu from './SelectMenu.vue';
import { LOCALES, language, languageSaved } from '../i18n';
import Sheet from './Sheet.vue';
import UpdatesPanel from './UpdatesPanel.vue';
import { Settings, Sun, Moon, Monitor, CircleHelp, Check } from '../icons';
import {
  theme,
  performanceMode,
  preferencesSaved,
  accent,
  activeAccent,
  ACCENT_PRESETS,
} from '../appearance';
import { oklch } from '../vendor/shelf/palette';
import { contrastRatio } from '../vendor/shelf/color';
import { backendOrigin, indexStatus } from '../indexer';
import { vTip } from '../vendor/shelf/hoverTip';
import { vGlassTabs } from '../glassTabs';
const { t } = useTranslation();
const emit = defineEmits<{ rebuild: []; help: [] }>();
const dialog = ref<InstanceType<typeof Sheet>>();
const themes = computed(
  () =>
    [
      { value: 'light', label: t('settings.light'), icon: Sun },
      { value: 'dark', label: t('settings.dark'), icon: Moon },
      { value: 'system', label: t('settings.system'), icon: Monitor },
    ] as const,
);
</script>
<template>
  <button
    type="button"
    class="icon-button utility-button"
    v-tip="t('settings.settings')"
    data-tip-placement="below"
    :aria-label="t('settings.settings')"
    aria-haspopup="dialog"
    @click="dialog?.showModal()"
  >
    <Settings :size="18" />
  </button>
  <Sheet
    ref="dialog"
    :title="t('settings.settings')"
    :close-label="t('settings.closeSettings')"
    class="settings-popup"
  >
    <div class="modal-inner">
      <section class="language-setting">
        <label for="interface-language" class="field-label">{{
          t('settings.language')
        }}</label>
        <SelectMenu
          id="interface-language"
          v-model="language"
          :aria-label="t('settings.language')"
          :options="[
            { value: 'system', label: t('settings.followSystem') },
            ...LOCALES.map((locale) => ({
              value: locale.id,
              label: locale.endonym,
            })),
          ]"
        />
      </section>
      <fieldset class="appearance-settings">
        <legend>{{ t('settings.appearance') }}</legend>
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
      <fieldset class="accent-settings">
        <legend>{{ t('settings.accent') }}</legend>
        <div
          class="accent-swatches"
          role="group"
          :aria-label="t('settings.accent')"
        >
          <button
            v-for="preset in ACCENT_PRESETS"
            :key="preset.id"
            type="button"
            class="accent-swatch"
            :aria-label="
              t(
                'settings.accent' +
                  preset.id[0].toUpperCase() +
                  preset.id.slice(1),
              )
            "
            :aria-pressed="activeAccent === preset.id"
            v-tip="
              t(
                'settings.accent' +
                  preset.id[0].toUpperCase() +
                  preset.id.slice(1),
              )
            "
            :style="{
              background: oklch(preset.seed),
              color:
                contrastRatio(preset.seed, { l: 0.99, c: 0, h: 0 }) >= 4.5
                  ? '#fff'
                  : '#111',
            }"
            @click="accent = preset.seed"
          >
            <Check v-if="activeAccent === preset.id" :size="16" />
          </button>
        </div>
      </fieldset>
      <label class="performance-setting">
        <span
          ><strong>{{ t('settings.performanceMode') }}</strong
          ><small>{{
            t('settings.opaqueSurfacesFewerVisualEffects')
          }}</small></span
        >
        <span class="settings-switch"
          ><input
            v-model="performanceMode"
            type="checkbox"
            role="switch"
            :aria-label="t('settings.performanceMode')" /><span
            class="switch-track"
            aria-hidden="true"
          ></span
        ></span>
      </label>
      <p class="settings-note">
        {{ t('settings.appliesImmediatelySavedOnThisBrowser') }}
      </p>
      <section v-if="backendOrigin" class="index-settings">
        <h3>{{ t('app.recordings') }}</h3>
        <p>
          {{ t('settings.refreshThumbnailsAndMetadataOriginalRecordingsStay') }}
        </p>
        <button
          class="button full-width"
          type="button"
          :disabled="indexStatus.driving !== 'parked' || indexStatus.running"
          @click="emit('rebuild')"
        >
          {{
            indexStatus.running
              ? t('settings.indexing')
              : t('settings.rebuildIndex')
          }}
        </button>
        <p v-if="indexStatus.driving !== 'parked'" class="settings-note">
          {{ t('settings.availableWhenTheVehicleIsConfirmedParked') }}
        </p>
        <p v-if="indexStatus.errors" class="settings-note" role="status">
          {{ t('settings.indexErrors', { count: indexStatus.errors }) }}
        </p>
      </section>
      <UpdatesPanel v-if="backendOrigin" />
      <button
        class="settings-help button full-width"
        type="button"
        @click="emit('help')"
      >
        <CircleHelp :size="18" />{{ t('app.helpAndInstallation') }}
      </button>
      <p v-if="!preferencesSaved || !languageSaved" role="status">
        {{ t('settings.browserStorageIsUnavailablePreferencesApplyFor') }}
      </p>
    </div>
  </Sheet>
</template>

<style scoped>
.language-setting {
  display: grid;
  gap: 8px;
  margin-bottom: 24px;
}

.accent-settings {
  margin: 20px 0;
  padding: 0;
  border: 0;
}
.accent-settings legend {
  margin-bottom: 10px;
  font-size: 13px;
  font-weight: 600;
}
.accent-swatches {
  display: grid;
  grid-template-columns: repeat(8, minmax(0, 1fr));
  gap: 8px;
}
.accent-swatch {
  display: grid;
  place-items: center;
  height: 32px;
  min-width: 0;
  border: 0;
  border-radius: var(--radius-control);
  transition: transform var(--t-press) var(--ease-out);
}
.accent-swatch[aria-pressed='true'] {
  box-shadow: var(--elev-thumb);
}
@media (hover: hover) and (pointer: fine) {
  .accent-swatch:hover {
    transform: scale(1.1);
  }
}
.accent-swatch:active {
  transform: scale(0.94);
}
@media (pointer: coarse) {
  .accent-swatches {
    grid-template-columns: repeat(4, minmax(0, 1fr));
  }
  .accent-swatch {
    height: 44px;
  }
}
@media (prefers-reduced-motion: reduce) {
  .accent-swatch {
    transition: none;
    transform: none;
  }
}
:global(:root[data-performance='true']) .accent-swatch {
  transition: none;
  transform: none;
}
</style>
