<script setup lang="ts">
import { ref, onMounted } from 'vue';
import SelectMenu from './SelectMenu.vue';
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
      throw new Error('State change failed. Reconnect to the sandbox.');
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
      <strong>Device sandbox</strong
      ><span>Synthetic recordings · no vehicle connected</span>
    </div>
    <label
      >Simulated state
      <SelectMenu
        :model-value="state"
        aria-label="Simulated state"
        :disabled="busy"
        :options="[
          { value: 'parked', label: 'Parked' },
          { value: 'onroad', label: 'Onroad' },
          { value: 'unknown', label: 'Unknown' },
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
.simulation-banner select {
  font-size: 16px;
  padding: 6px 10px;
  background: #fffaf0;
  border: 1px solid #c8bca1;
  border-radius: 8px;
  color: inherit;
}
</style>
