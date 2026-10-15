// Synthetic browser acceptance for camera preparation, GPS gaps and Shelf notices.
import { chromium, expect } from '@playwright/test';
import assert from 'node:assert/strict';
import AxeBuilder from '@axe-core/playwright';
import { installMapFixture } from './map-fixture.mjs';
const origin = process.env.DASH_TEST_URL || 'http://127.0.0.1:8091';
assert(
  (await (await fetch(`${origin}/api/info`)).json()).simulation,
  'This acceptance test uses only synthetic recordings',
);
const browser = await chromium.launch({
  executablePath:
    process.env.CHROME_PATH ||
    '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
  headless: true,
  args: [
    '--use-gl=angle',
    '--use-angle=swiftshader',
    '--enable-unsafe-swiftshader',
    '--renderer-process-limit=2',
    '--disable-accelerated-video-decode',
  ],
});
try {
  const context = await browser.newContext({
    viewport: { width: 1440, height: 900 },
  });
  await installMapFixture(context);
  await context.route('**/api/metadata?*', async (route) => {
    const response = await route.fetch();
    const entries = await response.json();
    for (const entry of Object.values(entries)) {
      const anchor = entry.metadata.startMonotonic;
      entry.metadata.points = [2, 3, 10, 11].map((offset) => ({
        latitude: 37 + offset * 0.0001,
        longitude: -122,
        time: 1700000000 + offset,
        monotonic: anchor + offset,
      }));
    }
    await route.fulfill({ response, json: entries });
  });
  const page = await context.newPage();
  const errors = [];
  page.on('pageerror', (error) => errors.push(error.message));
  await page.goto(origin);
  await page.waitForFunction(
    () => document.querySelector('video')?.readyState >= 2,
  );
  await expect(
    page.getByRole('button', { name: 'Follow', exact: true }),
  ).toBeEnabled();
  await expect(page.locator('.map-gps-gap')).toBeVisible();
  async function seek(time) {
    await page
      .getByRole('button', { name: 'Enter playback timestamp', exact: true })
      .click();
    await page
      .getByRole('textbox', { name: 'Playback timestamp', exact: true })
      .fill(time);
    await page.getByRole('button', { name: 'Go', exact: true }).click();
  }
  await seek('0:02.500');
  await expect(page.locator('.playback-map-marker')).toBeVisible();
  await expect(page.locator('.map-gps-gap')).toHaveCount(0);
  await seek('0:07.000');
  await expect(page.locator('.map-gps-gap')).toBeVisible();
  await expect(
    page.getByRole('button', { name: 'Follow', exact: true }),
  ).toBeEnabled();
  async function camera(name) {
    await page.getByRole('combobox', { name: 'Camera', exact: true }).click();
    await page.getByRole('option', { name, exact: true }).click();
  }
  let delay = true;
  await context.route('**/api/video?*', async (route) => {
    if (delay) {
      delay = false;
      await new Promise((resolve) => setTimeout(resolve, 1800));
    }
    await route.continue().catch(() => {});
  });
  await camera('fcamera.hevc');
  await expect(page.locator('.player-preparing')).toBeVisible();
  await expect(
    page.getByRole('button', { name: 'Play recording', exact: true }),
  ).toBeDisabled();
  // The camera selector remains usable before preparation completes.
  await camera('qcamera.ts');
  await page.waitForFunction(
    () => document.querySelector('video')?.readyState >= 2,
  );
  await expect(page.locator('.notice--error')).toHaveCount(0);
  await context.unroute('**/api/video?*');
  await camera('fcamera.hevc');
  await page.waitForFunction(
    () => document.querySelector('video')?.readyState >= 2,
  );
  await expect(page.locator('.player-preparing')).toHaveCount(0);
  // A failed readiness request has an explicit, dismissible local error.
  await context.route('**/api/playback/prepare?*', (route) =>
    route.fulfill({ status: 503, json: { error: 'test unavailable' } }),
  );
  await camera('ecamera.hevc');
  await expect(page.locator('.notice--error')).toBeVisible();
  assert((await page.locator('.notice--error').boundingBox()).width <= 385);
  const settings = page
    .getByRole('button', { name: 'Settings', exact: true })
    .filter({ visible: true });
  for (const mode of ['Light', 'Dark']) {
    for (const accent of [
      'Blue',
      'Purple',
      'Pink',
      'Red',
      'Orange',
      'Yellow',
      'Green',
      'Graphite',
    ]) {
      await settings.click();
      const dialog = page.getByRole('dialog', {
        name: 'Settings',
        exact: true,
      });
      await dialog.getByRole('radio', { name: mode, exact: true }).check();
      await dialog.getByRole('button', { name: accent, exact: true }).click();
      await dialog
        .getByRole('button', { name: 'Close settings', exact: true })
        .click();
      const report = await new AxeBuilder({ page })
        .include('.notices')
        .withRules(['color-contrast'])
        .analyze();
      assert.deepEqual(
        report.violations,
        [],
        `${mode}/${accent} notification contrast`,
      );
      assert.equal(
        await page
          .locator('.notice--error')
          .evaluate((e) => getComputedStyle(e).borderTopWidth),
        '0px',
      );
    }
  }
  await page
    .locator('.notice--error')
    .getByRole('button', { name: 'Dismiss', exact: true })
    .click();
  await expect(page.locator('.notice--error')).toHaveCount(0);
  await context.unroute('**/api/playback/prepare?*');
  await camera('qcamera.ts');
  await page.waitForFunction(
    () => document.querySelector('video')?.readyState >= 2,
  );
  for (const action of ['Bookmark drive', 'Remove bookmark']) {
    await page
      .getByRole('button', { name: 'Session actions', exact: true })
      .click();
    await page.getByRole('button', { name: action, exact: true }).click();
  }
  await expect(page.locator('.notice')).toHaveCount(2);
  await page
    .locator('.notice')
    .last()
    .getByRole('button', { name: 'Dismiss', exact: true })
    .click();
  await expect(page.locator('.notice')).toHaveCount(1);
  await page.locator('.notice').hover();
  await page.waitForTimeout(5200);
  await expect(page.locator('.notice')).toHaveCount(1);
  await page.setViewportSize({ width: 390, height: 844 });
  const notice = page.locator('.notice');
  const box = await notice.boundingBox();
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
  await page.mouse.down();
  await page.mouse.move(box.x + box.width + 100, box.y + box.height / 2, {
    steps: 5,
  });
  await page.mouse.up();
  await expect(page.locator('.notice')).toHaveCount(0);
  assert.equal(
    await page.evaluate(
      () => document.documentElement.scrollWidth > innerWidth,
    ),
    false,
  );
  assert.deepEqual(errors, []);
  console.log(
    'Passed: responsive HEVC preparation, rapid camera changes, dismissible failure, GPS gap/Follow, Shelf stack/close/hover pause/swipe, mobile width.',
  );
} finally {
  await browser.close();
}
