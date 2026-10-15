import { describe, expect, it } from 'vitest';
import { validateStyleMin } from '@maplibre/maplibre-gl-style-spec';
import defaults from '../public/map-config.json';
import { mapStyleForTheme, parseMapConfig } from './map';

describe('OSM vector maps', () => {
  it('accepts runtime Shortbread sources and rejects unsafe or mixed-content addresses', () => {
    expect(parseMapConfig(defaults, 'https://dash.local').tileJSON).toBe(
      'https://vector.openstreetmap.org/shortbread_v1/tilejson.json',
    );
    expect(
      parseMapConfig({ tileJSON: '/maps/tilejson.json' }, 'https://dash.local')
        .tileJSON,
    ).toBe('https://dash.local/maps/tilejson.json');
    for (const tileJSON of [
      'javascript:alert(1)',
      'file:///tmp/map',
      'https://user:password@example.com/maps',
      'http://example.com/maps',
    ])
      expect(() =>
        parseMapConfig({ tileJSON }, 'https://dash.local'),
      ).toThrow();
    for (const value of [null, {}, { tileJSON: 42 }])
      expect(() => parseMapConfig(value, 'https://dash.local')).toThrow();
  });
  it('validates both complete styles and the offline route style against the renderer schema', () => {
    for (const theme of ['light', 'dark'] as const) {
      for (const config of [defaults, undefined]) {
        const style = mapStyleForTheme(theme, config);
        expect(validateStyleMin(style).map((error) => error.message)).toEqual(
          [],
        );
        expect(style.glyphs).toBeUndefined();
        expect(style.sprite).toBeUndefined();
        expect(style.sources.route).toBeDefined();
        expect(JSON.stringify(style)).not.toMatch(
          /mapbox:|api\.mapbox|access_token/,
        );
      }
    }
  });
});
