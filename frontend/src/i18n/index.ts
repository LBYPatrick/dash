import i18next, { type TOptions } from 'i18next';
import I18NextVue from 'i18next-vue';
import { computed, ref, watch, type App } from 'vue';
import { readStored, writeStored } from '../storage';
import en from './locales/en-US.json';
import ja from './locales/ja.json';
import zh from './locales/zh-CN.json';
import ko from './locales/ko.json';
import vi from './locales/vi.json';

// Match Shelf: explicit native language names, system following, English fallback.
export const LOCALES = [
  { id: 'en-US', label: 'English', endonym: 'English' },
  { id: 'ja', label: 'Japanese', endonym: '日本語' },
  { id: 'zh-CN', label: 'Chinese (Simplified)', endonym: '简体中文' },
  { id: 'ko', label: 'Korean', endonym: '한국어' },
  { id: 'vi', label: 'Vietnamese', endonym: 'Tiếng Việt' },
] as const;
export type LocaleId = (typeof LOCALES)[number]['id'];
export type LanguagePreference = 'system' | LocaleId;
export function languagePreference(value: unknown): LanguagePreference {
  return LOCALES.some(({ id }) => id === value)
    ? (value as LocaleId)
    : 'system';
}
export function resolveLocale(
  preference: LanguagePreference,
  systemLocale: string,
): LocaleId {
  if (preference !== 'system') return preference;
  const normalized = systemLocale.toLowerCase().replaceAll('_', '-');
  const languageMap: Record<string, LocaleId> = {
    ja: 'ja',
    zh: 'zh-CN',
    ko: 'ko',
    vi: 'vi',
  };
  return (
    LOCALES.find(({ id }) => id.toLowerCase() === normalized)?.id ??
    languageMap[normalized.split('-')[0]] ??
    'en-US'
  );
}
const key = 'dash.language';
export const language = ref<LanguagePreference>(
  languagePreference(
    typeof window === 'undefined' ? 'system' : readStored(key, 'system'),
  ),
);
export const languageSaved = ref(true);
const systemLanguage = ref(
  typeof navigator === 'undefined' ? 'en-US' : navigator.language,
);
export const locale = computed(() =>
  resolveLocale(language.value, systemLanguage.value),
);
export const resources = {
  'en-US': { translation: en },
  ja: { translation: ja },
  'zh-CN': { translation: zh },
  ko: { translation: ko },
  vi: { translation: vi },
};
const ready = i18next.init({
  lng: locale.value,
  fallbackLng: 'en-US',
  resources,
  initAsync: false,
  interpolation: {
    escapeValue: false, // Vue escapes rendered text.
  },
  returnEmptyString: false,
});
export async function changeLocale(value: LocaleId) {
  await i18next.changeLanguage(value);
  if (typeof document !== 'undefined') {
    document.documentElement.lang = value;
    document.title = `${i18next.t('app.yourDrives')} · Dash`;
  }
}
export async function setupI18n(app: App) {
  await ready;
  await changeLocale(locale.value);
  app.use(I18NextVue, { i18next });
}
// Shared formatters outside components must participate in Vue dependency tracking.
export function t(key: string, options?: TOptions): string {
  void locale.value;
  return i18next.t(key, options) as string;
}
export function formatNumber(
  value: number,
  options?: Intl.NumberFormatOptions,
) {
  return new Intl.NumberFormat(locale.value, options).format(value);
}

watch(
  locale,
  (value) => {
    void changeLocale(value);
  },
  { flush: 'sync' },
);
let applyingStorage = false;
watch(
  language,
  (value) => {
    if (!applyingStorage && typeof window !== 'undefined')
      languageSaved.value = writeStored(key, value);
  },
  { flush: 'sync' },
);
if (typeof window !== 'undefined') {
  window.addEventListener('languagechange', () => {
    systemLanguage.value = navigator.language;
  });
  window.addEventListener('storage', (event) => {
    if (event.key !== key && event.key !== null) return;
    applyingStorage = true;
    language.value = languagePreference(readStored(key, 'system'));
    applyingStorage = false;
  });
}
export { i18next };
