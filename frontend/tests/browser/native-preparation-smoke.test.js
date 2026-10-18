import { chromium, expect } from '@playwright/test';
import { assert, test } from 'vite-plus/test';
import AxeBuilder from '@axe-core/playwright';
import { installMapFixture } from './map-fixture.js';

test('native camera preparation keeps the viewer usable', async () => {
  const origin = process.env.DASH_TEST_URL || 'http://127.0.0.1:8088';
  assert((await (await fetch(`${origin}/api/info`)).json()).simulation);
  const browser = await chromium.launch({
    executablePath: process.env.CHROME_PATH,
    headless: true,
    args: ['--disable-gpu', '--disable-accelerated-video-decode'],
  });
  try {
    for (const viewport of [
      { width: 1440, height: 900 },
      { width: 390, height: 844 },
    ]) {
      const context = await browser.newContext({ viewport });
      await installMapFixture(context);
      let pending = true,
        parked = true;
      const prepared = [];
      await context.route('**/api/index', async (route) => {
        const response = await route.fetch();
        const snapshot = await response.json();
        snapshot.status.driving = parked ? 'parked' : 'unknown';
        for (const entry of Object.values(snapshot.segments)) {
          if (pending)
            for (const file of entry.files)
              if (file.name === 'fcamera.hevc') delete file.native;
        }
        await route.fulfill({ response, json: snapshot });
      });
      await context.route('**/api/playback/prepare?*', async (route) => {
        const camera = new URL(route.request().url()).searchParams.get(
          'camera',
        );
        if (camera === 'fcamera.hevc') {
          prepared.push(camera);
          await route.fulfill({
            status: 200,
            json: { ready: false, status: 'indexing', completed: 0, total: 2 },
          });
        } else await route.continue();
      });
      const page = await context.newPage();
      const errors = [];
      page.on('pageerror', (error) => errors.push(error.message));
      await page.goto(origin);
      if (viewport.width < 600) {
        await page
          .getByRole('button', { name: 'Open navigation', exact: true })
          .click();
        await page
          .getByRole('dialog', { name: 'Navigation', exact: true })
          .getByRole('button', { name: 'Recordings', exact: true })
          .click();
        await page.locator('.drive-row:not(:disabled)').first().click();
      }
      await page.waitForFunction(
        () => document.querySelector('video')?.readyState >= 2,
      );
      const camera = page.getByRole('combobox', {
        name: 'Camera',
        exact: true,
      });
      await camera.click();
      await expect(
        page.getByRole('option', {
          name: 'fcamera.hevc · Indexing',
          exact: true,
        }),
      ).toHaveAttribute('aria-disabled', 'true');
      assert.equal(prepared.length, 0);
      await page.keyboard.press('Escape');
      await page
        .getByRole('button', { name: 'Session actions', exact: true })
        .click();
      await page
        .getByRole('button', {
          name: 'Prepare high-resolution video',
          exact: true,
        })
        .click();
      assert.equal(prepared.length, 1);
      assert(
        await page.locator('video').evaluate((video) => video.readyState >= 2),
      );
      // Once the parked index finishes, the same option unlocks without
      // replacing the playing road source or introducing a second viewer.
      pending = false;
      await camera.click();
      await expect(
        page.getByRole('option', { name: 'fcamera.hevc', exact: true }),
      ).not.toHaveAttribute('aria-disabled', 'true', { timeout: 10000 });
      await page.keyboard.press('Escape');
      pending = true;
      parked = false;
      await camera.click();
      await expect(
        page.getByRole('option', {
          name: 'fcamera.hevc · Park to prepare',
          exact: true,
        }),
      ).toHaveAttribute('aria-disabled', 'true', { timeout: 10000 });
      await page.keyboard.press('Escape');
      await page
        .getByRole('button', { name: 'Session actions', exact: true })
        .click();
      await expect(
        page.getByRole('button', {
          name: 'Prepare high-resolution video',
          exact: true,
        }),
      ).toBeDisabled();
      const audit = await new AxeBuilder({ page })
        .withTags(['wcag2a', 'wcag2aa', 'wcag21aa'])
        .analyze();
      assert.deepEqual(
        audit.violations
          .filter(
            (item) => item.impact === 'critical' || item.impact === 'serious',
          )
          .map((item) => item.id),
        [],
      );
      assert(
        await page.evaluate(
          () => document.documentElement.scrollWidth <= innerWidth,
        ),
      );
      assert.deepEqual(errors, []);
      await context.close();
    }
  } finally {
    await browser.close();
  }
});
