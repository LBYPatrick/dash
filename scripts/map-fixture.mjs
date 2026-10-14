import { readFileSync } from 'node:fs';
const tile = readFileSync(
  new URL('./fixtures/shortbread.mvt', import.meta.url),
);
// Keep repeatable UI tests independent of external services and avoid repeated
// viewport sweeps against OSM's donation-funded infrastructure. A separate
// manual/live check verifies the actual service; this geometry is synthetic.
export async function installMapFixture(context) {
  await context.route('https://vector.openstreetmap.org/**', (route) => {
    if (route.request().url().endsWith('tilejson.json'))
      return route.fulfill({
        json: {
          tilejson: '3.0.0',
          minzoom: 0,
          maxzoom: 14,
          tiles: [
            'https://vector.openstreetmap.org/shortbread_v1/{z}/{x}/{y}.mvt',
          ],
        },
      });
    return route.fulfill({
      body: tile,
      contentType: 'application/vnd.mapbox-vector-tile',
      headers: { 'cache-control': 'public, max-age=604800' },
    });
  });
}
