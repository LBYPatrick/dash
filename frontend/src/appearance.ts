import { computed, ref, watch } from 'vue';
import { readStored, writeStored } from './storage';
import {
  DEFAULT_ACCENT,
  readAccent,
  accentId,
  ACCENT_PRESETS,
} from './vendor/shelf/accents';
import { themeVariables, oklch, buildPalette } from './vendor/shelf/palette';
import { toHex } from './vendor/shelf/color';
export { ACCENT_PRESETS };
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
export const accent = ref(readAccent(saved.accent) ?? DEFAULT_ACCENT);
export const activeAccent = computed(() => accentId(accent.value) ?? 'blue');
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
  [resolvedTheme, performanceMode, accent],
  () => {
    const root = document.documentElement;
    root.dataset.theme = resolvedTheme.value;
    root.dataset.accent = activeAccent.value;
    for (const [key, value] of Object.entries(
      themeVariables(accent.value, resolvedTheme.value),
    ))
      root.style.setProperty(key, value);
    root.style.setProperty(
      '--color-primary-text',
      oklch({
        ...accent.value,
        l: resolvedTheme.value === 'dark' ? 0.82 : 0.44,
      }),
    );
    root.dataset.performance = String(performanceMode.value);
    root.style.colorScheme = resolvedTheme.value;
    document
      .querySelector('meta[name="theme-color"]')
      ?.setAttribute(
        'content',
        toHex(buildPalette(accent.value, resolvedTheme.value).base200),
      );
  },
  { immediate: true, flush: 'sync' },
);
let applyingStorage = false;
watch(
  [theme, performanceMode, accent],
  () => {
    if (applyingStorage) return;
    preferencesSaved.value = writeStored(key, {
      theme: theme.value,
      performance: performanceMode.value,
      accent: activeAccent.value,
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
  accent.value = readAccent(value?.accent) ?? DEFAULT_ACCENT;
  applyingStorage = false;
});
