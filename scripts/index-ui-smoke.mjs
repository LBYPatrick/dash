import { chromium } from '@playwright/test';
import assert from 'node:assert/strict';
import { installMapFixture } from './map-fixture.mjs';
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
  let building = false,
    rebuilds = 0,
    rejectRebuild = false;
  await context.route('**/api/index', async (route) => {
    const response = await route.fetch();
    const data = await response.json();
    data.status = {
      ...data.status,
      running: building,
      completed: building ? 2 : 6,
      total: 6,
      driving: 'parked',
    };
    await route.fulfill({ response, json: data });
  });
  await context.route('**/api/index/rebuild', async (route) => {
    rebuilds++;
    await route.fulfill(
      rejectRebuild
        ? {
            status: 409,
            json: {
              error: 'Rebuild unavailable while driving state is unknown.',
            },
          }
        : { json: { ok: true } },
    );
  });
  const page = await context.newPage();
  await page.goto(origin);
  await page.locator('.drive-row').first().waitFor();
  assert.equal(await page.locator('.index-activity').count(), 0);
  assert.equal(
    await page
      .getByRole('button', { name: 'Rebuild index', exact: true })
      .count(),
    0,
  );
  assert.equal(
    await page.getByText('Heavy processing runs only while parked').count(),
    0,
  );
  await page.getByRole('button', { name: 'Settings', exact: true }).click();
  await Promise.all([
    page.waitForResponse((r) => r.url().endsWith('/api/index/rebuild')),
    page.getByRole('button', { name: 'Rebuild index', exact: true }).click(),
  ]);
  assert.equal(rebuilds, 1);
  await page.getByRole('button', { name: 'Close settings' }).click();
  building = true;
  await page
    .getByRole('status')
    .filter({ hasText: 'Indexing 2 / 6' })
    .waitFor({ timeout: 8000 });
  const animation = await page.locator('.index-activity').evaluate((e) => {
    e.classList.add('index-activity-enter-active');
    return getComputedStyle(e).transitionDuration;
  });
  assert.match(animation, /0\.2s/);
  await page
    .locator('.index-activity')
    .evaluate((e) => e.classList.remove('index-activity-enter-active'));
  await page.screenshot({ path: '.artifacts/index-building.png' });
  building = false;
  await page
    .locator('.index-activity')
    .waitFor({ state: 'detached', timeout: 8000 });
  await page
    .getByRole('button', { name: 'Help and installation', exact: true })
    .click();
  await page
    .getByRole('heading', { name: 'Processing on your device' })
    .waitFor();
  assert.match(
    await page.getByRole('dialog').innerText(),
    /If driving state is unknown/,
  );
  await page.getByRole('button', { name: 'Close help' }).click();
  await page.emulateMedia({ reducedMotion: 'reduce' });
  building = true;
  await page.locator('.index-activity').waitFor({ timeout: 8000 });
  const reduced = await page.locator('.index-activity').evaluate((e) => {
    e.classList.add('index-activity-enter-active');
    return getComputedStyle(e).transitionDuration;
  });
  assert.equal(reduced, '0s');
  building = false;
  await page
    .locator('.index-activity')
    .waitFor({ state: 'detached', timeout: 8000 });
  rejectRebuild = true;
  await page.getByRole('button', { name: 'Settings', exact: true }).click();
  await page
    .getByRole('button', { name: 'Rebuild index', exact: true })
    .click();
  await page
    .getByRole('status')
    .filter({ hasText: 'Rebuild unavailable' })
    .waitFor();
  await page.getByRole('button', { name: 'Close settings' }).click();
  building = true;
  await page.locator('.index-activity').waitFor({ timeout: 8000 });
  await context.close();
  console.log(
    'Index UI passed: quiet idle state, Settings rebuild, Help explanation, animated activity and reduced-motion fallback.',
  );
} finally {
  await browser.close();
}
