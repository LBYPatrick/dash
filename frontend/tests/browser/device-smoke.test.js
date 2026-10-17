import { chromium } from '@playwright/test';
import { assert, test } from 'vite-plus/test';

// Explicit opt-in: this reads real recordings and briefly starts the owned live
// camera service. It never changes firmware state or rebuilds the index.
test.skipIf(process.env.DASH_DEVICE_ACCEPTANCE !== '1')(
  'physical device playback and stable indexing',
  async () => {
    const origin = process.env.DASH_TEST_URL;
    assert(origin && process.env.DASH_DEVICE_DRIVE);
    const info = await (await fetch(`${origin}/api/info`)).json();
    assert.equal(info.simulation, false);
    assert.equal(info.driving, 'parked');
    const before = await (await fetch(`${origin}/api/index`)).json();
    const browser = await chromium.launch({
      executablePath: process.env.CHROME_PATH,
      headless: true,
      args: [
        '--disable-gpu',
        '--disable-accelerated-video-decode',
        '--use-angle=swiftshader',
      ],
    });

    try {
      for (const viewport of [
        { width: 1440, height: 900 },
        { width: 390, height: 844 },
      ]) {
        const context = await browser.newContext({ viewport });
        const page = await context.newPage(),
          errors = [];
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
        }
        await page.getByRole('searchbox').fill(process.env.DASH_DEVICE_DRIVE);
        await page.locator('.drive-row:not(:disabled)').first().click();
        await page.waitForFunction(
          () => document.querySelector('video')?.readyState >= 2,
        );
        await page.locator('video').evaluate(async (video) => {
          video.muted = true;
          video.currentTime = 1;
          await video.play();
        });
        await page.waitForFunction(
          () => document.querySelector('video')?.currentTime > 2,
        );
        await page.locator('video').evaluate((video) => video.pause());
        await page
          .getByRole('combobox', { name: 'Camera', exact: true })
          .click();
        await page
          .getByRole('option', { name: 'fcamera.hevc', exact: true })
          .click();
        await page.waitForFunction(
          () => document.querySelector('video')?.readyState >= 2,
          undefined,
          { timeout: 120000 },
        );
        await page.locator('video').evaluate(async (video) => {
          video.muted = true;
          await video.play();
        });
        await page.waitForFunction(
          () => document.querySelector('video')?.currentTime > 3,
        );
        await page.locator('video').evaluate((video) => video.pause());
        assert(
          await page.evaluate(
            () => document.documentElement.scrollWidth <= innerWidth,
          ),
        );
        assert.deepEqual(errors, []);
        await context.close();
      }
      const after = await (await fetch(`${origin}/api/index`)).json();
      assert.equal(after.status.completed, before.status.completed);
      assert.equal(after.status.total, before.status.total);
    } finally {
      await browser.close();
    }
  },
);

test.skipIf(process.env.DASH_DEVICE_LIVE !== '1')(
  'physical device live view and browser recording',
  async () => {
    const origin = process.env.DASH_TEST_URL;
    assert(origin);
    const info = await (await fetch(`${origin}/api/info`)).json();
    assert.equal(info.simulation, false);
    assert.equal(info.driving, 'parked');
    const browser = await chromium.launch({
      executablePath: process.env.CHROME_PATH,
      headless: true,
      args: [
        '--disable-gpu',
        '--disable-accelerated-video-decode',
        '--use-angle=swiftshader',
      ],
    });
    try {
      const context = await browser.newContext({
        viewport: { width: 390, height: 844 },
        acceptDownloads: true,
      });
      const page = await context.newPage();
      await page.goto(origin);
      await page
        .getByRole('button', { name: 'Open navigation', exact: true })
        .click();
      await page
        .getByRole('dialog', { name: 'Navigation', exact: true })
        .getByRole('button', { name: 'Live', exact: true })
        .click();
      await page
        .getByRole('button', { name: 'Start live view', exact: true })
        .click();
      await page.waitForFunction(
        () =>
          Array.from(document.querySelectorAll('video')).some(
            (video) =>
              video.srcObject && video.readyState >= 2 && video.videoWidth > 0,
          ),
        undefined,
        { timeout: 90000 },
      );
      await page
        .getByRole('button', { name: 'Record this feed', exact: true })
        .click();
      const start = await page
        .locator('.live-player video')
        .evaluate((video) => video.currentTime);
      await page.waitForFunction(
        (time) =>
          Array.from(document.querySelectorAll('video')).some(
            (video) => video.srcObject && video.currentTime > time + 2,
          ),
        start,
      );
      await page
        .getByRole('button', { name: 'Stop recording', exact: true })
        .click();
      const download = page.waitForEvent('download');
      await page
        .getByRole('button', { name: 'Save recording', exact: true })
        .click();
      const clip = await download;
      assert.equal(await clip.failure(), null);
      if (process.env.DASH_DEVICE_OUTPUT)
        await clip.saveAs(`${process.env.DASH_DEVICE_OUTPUT}/live.webm`);
      // Closing without pressing Disconnect exercises keepalive/lease cleanup.
      await context.close();
    } finally {
      await browser.close();
    }
  },
);
