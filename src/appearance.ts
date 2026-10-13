import { computed, ref, watch } from 'vue';
import { readStored, writeStored } from './storage';
export type Theme = 'light' | 'dark' | 'system';
const key = 'dash.appearance';
const stored = readStored<unknown>(key, {});
const saved =
  stored && typeof stored === 'object'
    ? (stored as Record<string, unknown>)
    : {};
export const theme = ref<Theme>(
  saved.theme === 'light' || saved.theme === 'dark' ? saved.theme : 'system',
);
export const performanceMode = ref(saved.performance === true);
export const preferencesSaved = ref(true);
const system = window.matchMedia('(prefers-color-scheme: dark)');
const systemDark = ref(system.matches);
system.addEventListener('change', (event) => {
  systemDark.value = event.matches;
});
export const resolvedTheme = computed(() =>
  theme.value === 'system'
    ? systemDark.value
      ? 'dark'
      : 'light'
    : theme.value,
);
watch(
  [resolvedTheme, performanceMode],
  () => {
    const root = document.documentElement;
    root.dataset.theme = resolvedTheme.value;
    root.dataset.performance = String(performanceMode.value);
    root.style.colorScheme = resolvedTheme.value;
    document
      .querySelector('meta[name="theme-color"]')
      ?.setAttribute(
        'content',
        resolvedTheme.value === 'dark' ? '#111b22' : '#eef2f6',
      );
  },
  { immediate: true, flush: 'sync' },
);
let applyingStorage = false;
watch(
  [theme, performanceMode],
  () => {
    if (applyingStorage) return;
    preferencesSaved.value = writeStored(key, {
      theme: theme.value,
      performance: performanceMode.value,
    });
  },
  { flush: 'sync' },
);
window.addEventListener('storage', (event) => {
  if (event.key !== key && event.key !== null) return;
  const value = readStored<Record<string, unknown> | null>(key, null);
  applyingStorage = true;
  theme.value =
    value?.theme === 'light' || value?.theme === 'dark'
      ? value.theme
      : 'system';
  performanceMode.value = value?.performance === true;
  applyingStorage = false;
});
