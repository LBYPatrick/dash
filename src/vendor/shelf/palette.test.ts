import { describe, it, expect } from 'vitest';
import {
  ACCENT_PRESETS,
  DEFAULT_ACCENT,
  readAccent,
  accentId,
} from './accents';
import { buildPalette } from './palette';
import { contrastRatio } from './color';
describe('Shelf accent palettes', () => {
  it('defaults to blue and accepts only named or legacy matching presets', () => {
    expect(accentId(DEFAULT_ACCENT)).toBe('blue');
    expect(readAccent('purple')).toEqual(ACCENT_PRESETS[1].seed);
    expect(readAccent({ ...ACCENT_PRESETS[2].seed })).toEqual(
      ACCENT_PRESETS[2].seed,
    );
    expect(readAccent({ l: 1, c: 0, h: 0 })).toBeUndefined();
    expect(readAccent('unknown')).toBeUndefined();
  });
  for (const mode of ['light', 'dark'] as const)
    for (const preset of ACCENT_PRESETS) {
      it(`${preset.id}/${mode} keeps readable surfaces and filled controls`, () => {
        const palette = buildPalette(preset.seed, mode);
        expect(
          contrastRatio(palette.baseContent, palette.base100),
        ).toBeGreaterThanOrEqual(4.5);
        expect(
          contrastRatio(palette.primaryContent, palette.primary),
        ).toBeGreaterThanOrEqual(4.5);
        expect(
          contrastRatio(palette.primary, palette.base100),
        ).toBeGreaterThanOrEqual(3);
      });
    }
});
