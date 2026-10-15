import type { ExpressionSpecification, StyleSpecification } from 'maplibre-gl';

export const osmAttribution =
  '© <a href="https://www.openstreetmap.org/copyright" target="_blank" rel="noopener">OpenStreetMap contributors</a> · <a href="https://www.openstreetmap.org/fixthemap" target="_blank" rel="noopener">Report a map issue</a>';
export interface MapConfig {
  tileJSON: string;
}
export function parseMapConfig(value: unknown, origin: string): MapConfig {
  if (
    !value ||
    typeof value !== 'object' ||
    !('tileJSON' in value) ||
    typeof value.tileJSON !== 'string' ||
    !value.tileJSON.trim()
  )
    throw new Error('Invalid map configuration');
  const url = new URL(value.tileJSON, origin);
  if (
    !['http:', 'https:'].includes(url.protocol) ||
    url.username ||
    url.password ||
    (new URL(origin).protocol === 'https:' && url.protocol !== 'https:')
  )
    throw new Error('Invalid map tile address');
  return { tileJSON: url.href };
}

// Dash's own restrained Shortbread style. No proprietary styles, remote fonts,
// sprites or API keys. Omitting glyphs lets MapLibre use local system fonts.
export function mapStyleForTheme(
  theme: 'light' | 'dark',
  config?: MapConfig,
): StyleSpecification {
  const dark = theme === 'dark';
  const colors = dark
    ? {
        land: '#192126',
        water: '#122f40',
        green: '#253c32',
        building: '#29343d',
        edge: '#34434e',
        road: '#53636f',
        highway: '#6c7880',
        label: '#d8e2e9',
        minor: '#b3c1cb',
        route: '#a4e8cd',
      }
    : {
        land: '#f3f4f2',
        water: '#c7dfe8',
        green: '#dce8d9',
        building: '#e1e4e3',
        edge: '#d6dcdf',
        road: '#ffffff',
        highway: '#ffffff',
        label: '#3d494f',
        minor: '#5b6870',
        route: '#286b50',
      };
  const layers: StyleSpecification['layers'] = [
    {
      id: 'background',
      type: 'background',
      paint: { 'background-color': colors.land },
    },
  ];
  const sources: StyleSpecification['sources'] = {};
  if (config) {
    sources.osm = {
      type: 'vector',
      url: config.tileJSON,
      attribution: osmAttribution,
    };
    for (const layer of ['ocean', 'water_polygons'])
      layers.push({
        id: layer,
        type: 'fill',
        source: 'osm',
        'source-layer': layer,
        paint: { 'fill-color': colors.water },
      });
    layers.push(
      {
        id: 'green',
        type: 'fill',
        source: 'osm',
        'source-layer': 'land',
        filter: [
          'in',
          ['get', 'kind'],
          [
            'literal',
            [
              'wood',
              'forest',
              'grass',
              'grassland',
              'meadow',
              'scrub',
              'heath',
              'park',
              'nature_reserve',
            ],
          ],
        ],
        paint: { 'fill-color': colors.green },
      },
      {
        id: 'parks',
        type: 'fill',
        source: 'osm',
        'source-layer': 'sites',
        filter: [
          'in',
          ['get', 'kind'],
          [
            'literal',
            [
              'park',
              'garden',
              'golf_course',
              'recreation_ground',
              'pitch',
              'cemetery',
            ],
          ],
        ],
        paint: { 'fill-color': colors.green },
      },
      {
        id: 'waterways',
        type: 'line',
        source: 'osm',
        'source-layer': 'water_lines',
        paint: {
          'line-color': colors.water,
          'line-width': ['interpolate', ['linear'], ['zoom'], 8, 0.5, 16, 3],
        },
      },
      {
        id: 'buildings',
        type: 'fill',
        source: 'osm',
        'source-layer': 'buildings',
        minzoom: 14,
        paint: {
          'fill-color': colors.building,
          'fill-outline-color': colors.edge,
        },
      },
    );
    const roadWidth = (extra = 0): ExpressionSpecification => [
      'interpolate',
      ['exponential', 1.5],
      ['zoom'],
      6,
      0.4 + extra,
      12,
      2 + extra,
      16,
      7 + extra,
      19,
      24 + extra,
    ];
    for (const [id, casing] of [
      ['road-casing', true],
      ['roads', false],
    ] as const)
      layers.push({
        id,
        type: 'line',
        source: 'osm',
        'source-layer': 'streets',
        filter: ['!=', ['get', 'rail'], true],
        layout: { 'line-cap': 'round', 'line-join': 'round' },
        paint: {
          'line-color': casing
            ? colors.edge
            : [
                'match',
                ['get', 'kind'],
                ['motorway', 'trunk', 'primary'],
                colors.highway,
                colors.road,
              ],
          'line-width': roadWidth(casing ? 1.5 : 0),
          'line-opacity': ['case', ['==', ['get', 'tunnel'], true], 0.5, 1],
        },
      });
    layers.push({
      id: 'boundaries',
      type: 'line',
      source: 'osm',
      'source-layer': 'boundaries',
      paint: {
        'line-color': colors.minor,
        'line-width': 0.8,
        'line-opacity': 0.4,
        'line-dasharray': [3, 3],
      },
    });
  }
  sources.route = {
    type: 'geojson',
    data: { type: 'FeatureCollection', features: [] },
  };
  layers.push(
    {
      id: 'routeOutline',
      type: 'line',
      source: 'route',
      layout: { 'line-cap': 'round', 'line-join': 'round' },
      paint: { 'line-color': dark ? '#192126' : '#ffffff', 'line-width': 7 },
    },
    {
      id: 'routeLine',
      type: 'line',
      source: 'route',
      layout: { 'line-cap': 'round', 'line-join': 'round' },
      paint: { 'line-color': colors.route, 'line-width': 3 },
    },
  );
  if (config) {
    const name: ExpressionSpecification = [
      'coalesce',
      ['get', 'name'],
      ['get', 'name_en'],
      '',
    ];
    for (const [id, sourceLayer, minzoom, size] of [
      ['street-names', 'street_labels', 13, 11],
      ['place-names', 'place_labels', 4, 13],
    ] as const)
      layers.push({
        id,
        type: 'symbol',
        source: 'osm',
        'source-layer': sourceLayer,
        minzoom,
        layout: {
          'symbol-placement': id === 'street-names' ? 'line' : 'point',
          'text-field': name,
          'text-font': ['sans-serif'],
          'text-size': size,
          'text-max-width': 9,
          'text-padding': 4,
        },
        paint: {
          'text-color': id === 'street-names' ? colors.minor : colors.label,
          'text-halo-color': colors.land,
          'text-halo-width': 1.5,
        },
      });
  }
  return {
    version: 8,
    name: `Dash ${theme}`,
    sources,
    layers,
    transition: { duration: 0, delay: 0 },
  };
}
