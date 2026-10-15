<script setup lang="ts">
import { useTranslation } from 'i18next-vue';
import { ref, onMounted } from 'vue';
import SelectMenu from './SelectMenu.vue';
const { t } = useTranslation();
const enabled = ref(false);
const state = ref('unknown');
const busy = ref(false);
const error = ref('');
onMounted(async () => {
  try {
    const response = await fetch('/api/info');
    const info = await response.json();
    enabled.value = info.simulation === true;
    state.value = info.driving;
  } catch {
    /* No simulator on a disconnected sample library. */
  }
});
async function change(requested: string) {
  busy.value = true;
  error.value = '';
  try {
    const response = await fetch('/api/simulation/state', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ state: requested }),
    });
    if (!response.ok)
      throw new Error(t('sandbox.stateChangeFailedReconnectToTheSandbox'));
    state.value = (await response.json()).driving;
  } catch (cause) {
    error.value = (cause as Error).message;
  } finally {
    busy.value = false;
  }
}
</script>
<template>
  <aside v-if="enabled" class="simulation-banner">
    <div>
      <strong>{{ t('sandbox.deviceSandbox') }}</strong
      ><span>{{ t('sandbox.syntheticRecordingsNoVehicleConnected') }}</span>
    </div>
    <label
      ><span class="simulation-state-label">{{
        t('sandbox.simulatedState')
      }}</span>
      <SelectMenu
        :model-value="state"
        :aria-label="t('sandbox.simulatedState')"
        :disabled="busy"
        :options="[
          { value: 'parked', label: t('sandbox.parked') },
          { value: 'onroad', label: t('sandbox.onroad') },
          { value: 'unknown', label: t('sandbox.unknown') },
        ]"
        @update:model-value="change"
    /></label>
    <span v-if="error" role="alert">{{ error }}</span>
  </aside>
</template>
<style scoped>
.simulation-banner {
  display: flex;
  gap: 16px;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  padding: 12px 24px;
  background: #f6ebd2;
  color: #544b35;
  border-bottom: 1px solid #dfd2b4;
  font-size: 13px;
}
.simulation-banner strong,
.simulation-banner span {
  display: block;
}
.simulation-banner span {
  font-size: 12px;
  margin-top: 3px;
}
.simulation-banner label {
  display: flex;
  align-items: center;
  gap: 10px;
}
.simulation-state-label {
  flex-shrink: 0;
}
.simulation-banner select {
  font-size: 16px;
  padding: 6px 10px;
  background: #fffaf0;
  border: 1px solid #c8bca1;
  border-radius: 8px;
  color: inherit;
}
@media (max-width: 900px) {
  .simulation-banner {
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto;
    padding: 4px 14px;
    gap: 8px;
  }
  .simulation-banner > div > span,
  .simulation-state-label {
    display: none;
  }
  .simulation-banner > label {
    min-width: 0;
  }
  .simulation-banner > span[role='alert'] {
    grid-column: 1 / -1;
  }
  :global(#app .simulation-banner .selectmenu) {
    width: 124px;
    max-width: 40vw;
  }
  :global(#app .simulation-banner .select-trigger) {
    min-height: 44px;
  }
}
</style>
