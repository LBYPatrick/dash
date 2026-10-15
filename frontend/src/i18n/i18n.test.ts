import { afterEach, describe, expect, it } from 'vitest';
import { computed } from 'vue';
import { readFileSync, readdirSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { baseParse } from '@vue/compiler-dom';
import { parse } from '@vue/compiler-sfc';
import {
  resources,
  LOCALES,
  language,
  languagePreference,
  locale,
  resolveLocale,
  t,
  i18next,
  formatNumber,
} from './index';
import { backendMessage } from './backend';
import { activityBuckets } from '../statistics';
import { engagementAt } from '../viewerTimeline';

type Bundle = Record<string, Record<string, string>>;
const en = resources['en-US'].translation as Bundle;
const flatten = (bundle: Bundle) =>
  Object.fromEntries(
    Object.entries(bundle).flatMap(([section, entries]) =>
      Object.entries(entries).map(([key, value]) => [
        `${section}.${key}`,
        value,
      ]),
    ),
  );
const source = flatten(en);
const placeholders = (value: string) =>
  [...value.matchAll(/{{\s*([^}]+)\s*}}/g)]
    .map((m) => m[1].replace(/\s/g, ''))
    .sort();
afterEach(async () => {
  language.value = 'en-US';
  await i18next.changeLanguage('en-US');
});

describe('Shelf-compatible catalogs', () => {
  for (const { id } of LOCALES) {
    it(`${id} covers English keys and preserves interpolation`, () => {
      const bundle = flatten(resources[id].translation as Bundle);
      expect(Object.keys(bundle).sort()).toEqual(Object.keys(source).sort());
      for (const [key, value] of Object.entries(bundle)) {
        expect(value.trim(), key).not.toBe('');
        expect(placeholders(value), key).toEqual(placeholders(source[key]));
      }
      if (id !== 'en-US')
        expect(
          Object.keys(bundle).filter((key) => source[key] === bundle[key])
            .length,
        ).toBeLessThan(25);
    });
  }
  it('uses language subtags and safe defaults like Shelf', () => {
    for (const [reported, expected] of [
      ['ja-JP', 'ja'],
      ['zh-Hans-CN', 'zh-CN'],
      ['zh-TW', 'zh-CN'],
      ['ko-KR', 'ko'],
      ['vi-VN', 'vi'],
      ['de-DE', 'en-US'],
      ['en-GB', 'en-US'],
      ['JA_jp', 'ja'],
    ]) {
      expect(resolveLocale('system', reported)).toBe(expected);
    }
    expect(resolveLocale('ko', 'ja-JP')).toBe('ko');
    for (const malformed of [null, {}, 'cimode', 'unknown', 0])
      expect(languagePreference(malformed)).toBe('system');
  });
  it('pluralizes complete phrases and never leaves placeholders in the UI', () => {
    language.value = 'en-US';
    expect(t('common.drives', { count: 1 })).toBe('1 drive');
    expect(t('common.drives', { count: 2 })).toBe('2 drives');
    for (const { id } of LOCALES) {
      language.value = id;
      for (const count of [0, 1, 2, 1500]) {
        expect(t('common.drives', { count })).not.toMatch(/{{|common\./);
        expect(t('settings.indexErrors', { count })).not.toMatch(
          /{{|settings\./,
        );
      }
    }
  });
  it('keeps helper output and cached computed statistics reactive to language', () => {
    const label = computed(() => t('app.recordings'));
    const number = computed(() => formatNumber(1234.5));
    const row = {
      drive: {
        id: 'test',
        date: Date.UTC(2026, 9, 14, 12),
        dated: true,
        segments: [],
      },
      distance: 0,
      duration: 0,
      covered: 0,
      distanceParts: 0,
      durationParts: 0,
      total: 0,
    };
    const buckets = computed(() => activityBuckets([row], 'month'));
    language.value = 'en-US';
    expect(label.value).toBe('Recordings');
    const english = buckets.value[0].label;
    language.value = 'vi';
    expect(locale.value).toBe('vi');
    expect(label.value).toBe('Bản ghi');
    expect(number.value).toBe('1.234,5');
    expect(buckets.value[0].label).not.toBe(english);
    expect(engagementAt([], 0)).toBe('Không ghi trạng thái điều khiển');
    expect(
      backendMessage('this firmware lacks managed local streaming; detail'),
    ).not.toMatch(/^this firmware/);
    expect(backendMessage('diagnostic: unexpected packet 7')).toBe(
      'diagnostic: unexpected packet 7',
    );
  });
});

const files: string[] = [];
function walk(folder: string) {
  for (const file of readdirSync(folder, { withFileTypes: true })) {
    const path = join(folder, file.name);
    if (file.isDirectory()) {
      if (file.name !== 'vendor') walk(path);
    } else if (/\.(vue|ts)$/.test(path) && !path.includes('.test.'))
      files.push(path);
  }
}
walk(fileURLToPath(new URL('../', import.meta.url)));
describe('interface translation coverage', () => {
  it('has every literal key requested by a component or formatter', () => {
    const known = new Set(
      Object.keys(source).flatMap((key) => [
        key,
        key.replace(/_(zero|one|two|few|many|other)$/, ''),
      ]),
    );
    let count = 0;
    for (const file of files)
      for (const [, key] of readFileSync(file, 'utf8').matchAll(
        /\bt\(\s*'([\w]+\.[\w]+)'/g,
      )) {
        expect(known.has(key), `${file}: ${key}`).toBe(true);
        count++;
      }
    expect(count).toBeGreaterThan(300);
  });
  it('keeps translatable Vue text and accessibility labels out of templates', () => {
    const allowed = new Set(['dash', 'm:ss.SSS']);
    const leftovers: string[] = [];
    for (const file of files.filter((f) => f.endsWith('.vue'))) {
      const template = parse(readFileSync(file, 'utf8')).descriptor.template;
      if (!template) continue;
      const visit = (node: any) => {
        if (node.type === 2) {
          const text = node.content.replace(/\s+/g, ' ').trim();
          if (/[A-Za-z]/.test(text) && !allowed.has(text))
            leftovers.push(`${file}: ${text}`);
        }
        for (const prop of node.props || [])
          if (
            prop.type === 6 &&
            prop.value &&
            /^(aria-label|title|label|close-label|alt|placeholder)$/.test(
              prop.name,
            ) &&
            /[A-Za-z]/.test(prop.value.content) &&
            !allowed.has(prop.value.content)
          )
            leftovers.push(`${file}: ${prop.name}=${prop.value.content}`);
        for (const child of node.children || []) visit(child);
      };
      visit(baseParse(template.content));
    }
    expect(leftovers).toEqual([]);
  });
});
