import type { StyleSpecification } from 'mapbox-gl';
// Browser map configuration is supplied by the deployer's ignored .env.
export const mapboxToken = import.meta.env.VITE_MAPBOX_TOKEN || '';
export const mapStyleForTheme = (
  theme: 'light' | 'dark',
): string | StyleSpecification => {
  if (import.meta.env.VITE_MAPBOX_STYLE)
    return import.meta.env.VITE_MAPBOX_STYLE;
  if (mapboxToken) return `mapbox://styles/mapbox/${theme}-v11`;
  // Keep automatic maps usable in fresh installations without credentials.
  return {
    version: 8,
    sources: {
      osm: {
        type: 'raster',
        tiles: ['https://tile.openstreetmap.org/{z}/{x}/{y}.png'],
        tileSize: 256,
        maxzoom: 19,
        attribution:
          '© <a href="https://www.openstreetmap.org/copyright">OpenStreetMap contributors</a>',
      },
    },
    layers: [
      {
        id: 'osm',
        type: 'raster',
        source: 'osm',
        paint: {
          'raster-saturation': theme === 'dark' ? -0.7 : -0.25,
          'raster-brightness-max': theme === 'dark' ? 0.65 : 1,
        },
      },
    ],
  };
};
