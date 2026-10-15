import { installMapFixture } from './map-fixture.js';
import { chromium } from '@playwright/test';
import { assert, test } from 'vite-plus/test';

test('mobile-scroll-smoke', async () => {
  const origin = process.env.DASH_TEST_URL || 'http://127.0.0.1:8088';
  const browser = await chromium.launch({
    executablePath:
      process.env.CHROME_PATH ||
      '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
    headless: true,
  });
  try {
    const context = await browser.newContext({
      viewport: { width: 390, height: 844 },
      isMobile: true,
      hasTouch: true,
    });
    await installMapFixture(context);
    const page = await context.newPage();
    const errors = [];
    page.on('pageerror', (e) => errors.push(e.message));
    const client = await context.newCDPSession(page);
    async function swipe(x, from, to) {
      await client.send('Input.synthesizeScrollGesture', {
        x,
        y: from,
        yDistance: to - from,
        gestureSourceType: 'touch',
        speed: 600,
      });
      await page.waitForTimeout(200);
    }
    async function navigate(view) {
      await page.getByRole('button', { name: 'Open navigation' }).tap();
      await page
        .getByRole('dialog', { name: 'Navigation', exact: true })
        .getByRole('button', { name: view, exact: true })
        .tap();
    }
    await page.goto(origin);
    await page.getByRole('heading', { name: 'Your drives' }).waitFor();
    await navigate('Overview');
    await page.getByRole('heading', { name: 'Recording overview' }).waitFor();
    for (const [width, height] of [
      [390, 844],
      [844, 390],
      [390, 650],
    ]) {
      await page.setViewportSize({ width, height });
      await page.waitForTimeout(100);
      const geometry = await page.locator('.detail').evaluate((e) => ({
        client: e.clientHeight,
        scroll: e.scrollHeight,
        available: e.parentElement.clientHeight,
      }));
      assert(
        geometry.client <= geometry.available + 1,
        'Detail pages must fit the workspace instead of being clipped',
      );
      assert(
        geometry.scroll > geometry.client + 100,
        'The dashboard fixture must overflow',
      );
      await page.locator('.detail').evaluate((e) => (e.scrollTop = 0));
      // Keep the gesture origin within the shared portrait/landscape widget area.
      await swipe(Math.min(width / 2, 180), 300, 120);
      assert(
        await page.locator('.detail').evaluate((e) => e.scrollTop > 80),
        `Touch swipes must scroll the dashboard at ${width}x${height}: ${JSON.stringify(await page.locator('.detail').evaluate((e) => ({ top: e.scrollTop, height: e.clientHeight, scroll: e.scrollHeight, touch: getComputedStyle(e).touchAction })))}`,
      );
      await page
        .locator('.detail')
        .evaluate((e) => (e.scrollTop = e.scrollHeight));
      const notes = await page.locator('.statistics-notes').boundingBox();
      assert(
        notes.y + notes.height <= height + 1,
        'The bottom of the dashboard must be reachable',
      );
      assert(
        await page.evaluate(() => document.scrollingElement.scrollTop === 0),
        'App scrolling must not move the outer document',
      );
    }
    await page.setViewportSize({ width: 390, height: 844 });
    await navigate('Recordings');
    // Browser-only repeated rows test a long library without adding device files.
    await page.locator('.drive-scroll').evaluate((e) => {
      const row = e.querySelector('.drive-row');
      for (let i = 0; i < 40; i++) e.append(row.cloneNode(true));
    });
    const listBounds = await page.locator('.drive-scroll').boundingBox();
    const listStart = listBounds.y + Math.min(100, listBounds.height * 0.6);
    await swipe(180, listStart, listStart - 160);
    assert(
      await page.locator('.drive-scroll').evaluate((e) => e.scrollTop > 80),
      'Touch swipes must scroll long drive lists',
    );
    await page.locator('.drive-scroll').evaluate((e) => (e.scrollTop = 0));
    await page.locator('.drive-row').first().tap();
    await page.getByRole('button', { name: 'Video', exact: true }).waitFor();
    await swipe(180, 300, 120);
    assert(
      await page
        .locator('.session-view')
        .evaluate(
          (e) =>
            getComputedStyle(e).overflowY === 'hidden' && e.scrollTop === 0,
        ),
      'The session viewer must stay fixed',
    );
    await page.getByRole('button', { name: 'Settings', exact: true }).tap();
    await page.getByRole('dialog', { name: 'Settings', exact: true }).waitFor();
    await page.getByRole('button', { name: 'Close settings' }).tap();
    await page
      .getByRole('dialog', { name: 'Settings', exact: true })
      .waitFor({ state: 'hidden' });
    assert(
      await page.evaluate(() => document.scrollingElement.scrollTop === 0),
      'Closing a sheet must not shift the app document',
    );
    assert.deepEqual(errors, []);
    console.log(
      'Mobile scroll passed: native Chromium touch gestures scroll dashboard and long library; portrait/landscape/resized viewports reach the bottom; viewer and outer shell stay fixed after sheet dismissal. Physical-device Safari remains unverified.',
    );
  } finally {
    await browser.close();
  }
});
