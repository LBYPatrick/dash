import { chromium } from '@playwright/test';
import { assert, test } from 'vite-plus/test';
import { installMapFixture } from './map-fixture.js';

test('map-smoke', async () => {
  const origin = process.env.DASH_TEST_URL || 'http://127.0.0.1:8088';
  const browser = await chromium.launch({
    executablePath:
      process.env.CHROME_PATH ||
      '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
    headless: true,
  });
  try {
    const context = await browser.newContext({
      viewport: { width: 1440, height: 900 },
    });
    await installMapFixture(context);
    const page = await context.newPage();
    const errors = [],
      forbidden = [];
    page.on('pageerror', (e) => errors.push(e.message));
    page.on('request', (request) => {
      if (/\b(?:mapbox\.com|mapbox\.cn)\b/.test(request.url()))
        forbidden.push(request.url());
    });
    const tileLoaded = page.waitForResponse((r) => r.url().endsWith('.mvt'));
    await page.goto(origin);
    await page.locator('.playback-map-marker').waitFor();
    await tileLoaded;
    assert.match(
      await page.locator('.maplibregl-ctrl-attrib').innerText(),
      /OpenStreetMap contributors/,
    );
    assert(
      await page.getByRole('link', { name: 'Report a map issue' }).isVisible(),
    );
    const canvas = await page.locator('.maplibregl-canvas').elementHandle();
    await page.getByRole('button', { name: 'Settings', exact: true }).click();
    await page.getByRole('radio', { name: 'Dark', exact: true }).check();
    await page.getByRole('button', { name: 'Close settings' }).click();
    assert(
      await canvas.evaluate((e) => e.isConnected),
      'Theme changes must preserve the map instance',
    );
    await page.screenshot({ path: '.artifacts/maplibre-dark.png' });
    // Tile failure must preserve the independently supplied GPS route and marker.
    await page.route('https://vector.openstreetmap.org/**', (route) =>
      route.abort(),
    );
    await page.reload();
    await page.locator('.map-notice').waitFor();
    await page.locator('.playback-map-marker').waitFor();
    assert(
      await page
        .getByRole('button', { name: 'Full route', exact: true })
        .isEnabled(),
    );
    await page.screenshot({ path: '.artifacts/maplibre-unavailable.png' });
    // Unavailable WebGL still presents the recording's own GPS geometry.
    await page.addInitScript(() => {
      // oxlint-disable-next-line typescript/unbound-method -- Keep the native method to invoke with the canvas as this below.
      const original = HTMLCanvasElement.prototype.getContext;
      HTMLCanvasElement.prototype.getContext = function (type, ...args) {
        return /webgl/.test(type) ? null : original.call(this, type, ...args);
      };
    });
    await page.reload();
    await page
      .getByRole('img', { name: 'Recorded route without map background' })
      .waitFor();
    assert(
      (
        await page
          .locator('.map-route-fallback polyline')
          .getAttribute('points')
      ).length > 10,
    );
    await context.close();
    // Hidden phone panels must not initialize or request a basemap.
    const phoneContext = await browser.newContext({
      viewport: { width: 390, height: 844 },
      isMobile: true,
      hasTouch: true,
    });
    await installMapFixture(phoneContext);
    const phone = await phoneContext.newPage();
    let requests = 0;
    phone.on('request', (request) => {
      if (request.url().includes('vector.openstreetmap.org')) requests++;
    });
    await phone.goto(origin);
    await phone.locator('.drive-row').first().tap();
    await phone.locator('video').waitFor();
    await phone.waitForTimeout(250);
    assert.strictEqual(
      requests,
      0,
      'Hidden mobile maps must not request tiles',
    );
    await phone.getByRole('button', { name: 'Map & data', exact: true }).tap();
    await phone.locator('.playback-map-marker').waitFor();
    assert(requests > 0);
    const region = await phone.locator('.map-canvas').boundingBox();
    const attribution = await phone
      .locator('.maplibregl-ctrl-attrib')
      .boundingBox();
    assert(
      attribution.y + attribution.height <= region.y + region.height + 1,
      'Attribution must fit the visible map',
    );
    await phoneContext.close();
    assert.deepEqual(errors, []);
    assert.deepEqual(forbidden, []);
    console.log(
      'MapLibre passed: OSM vector tiles, local fonts, attribution, stable theme changes, tile/WebGL failure, mobile lazy initialization, and no Mapbox requests.',
    );
  } finally {
    await browser.close();
  }
});
