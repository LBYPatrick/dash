import { chromium } from '@playwright/test';
import assert from 'node:assert/strict';
import { writeFile } from 'node:fs/promises';
const origin = process.env.DASH_TEST_URL || 'http://127.0.0.1:8088';
const simulation =
  (await (await fetch(`${origin}/api/info`)).json()).simulation === true;
async function setDriving(state) {
  if (simulation) {
    const response = await fetch(`${origin}/api/simulation/state`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ state }),
    });
    assert.equal(response.status, 200);
  } else {
    await writeFile(
      '.artifacts/device/params/IsOnroad',
      state === 'onroad' ? '1' : '0',
    );
  }
}
if (simulation) await setDriving('parked');
const browser = await chromium.launch({
  executablePath:
    process.env.CHROME_PATH ||
    '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
  headless: true,
});
const errors = [];
try {
  const context = await browser.newContext({
    viewport: { width: 1440, height: 1080 },
    acceptDownloads: true,
  });
  const page = await context.newPage();
  page.on('pageerror', (error) => errors.push(error.message));
  await page.goto(origin);
  await page.getByRole('heading', { name: 'Your drives' }).waitFor();
  assert.equal(await page.locator('.topbar .workspace-nav').isVisible(), true);
  assert.equal(await page.locator('.sidebar .workspace-nav').count(), 0);
  await page.waitForFunction(
    () => document.querySelector('video')?.readyState >= 1,
  );
  await page.waitForFunction(
    () => Math.abs(document.querySelector('video').duration - 16) < 0.2,
  );
  assert.equal(await page.getByLabel('Choose recording segment').count(), 0);
  await page.locator('video').evaluate(async (video) => {
    video.currentTime = 7.8;
    await video.play();
  });
  await page.waitForFunction(
    () => document.querySelector('video').currentTime > 8.5,
  );
  await page.locator('video').evaluate((video) => video.pause());
  await page.locator('video').evaluate((video) => {
    video.currentTime = 12;
  });
  await page.waitForFunction(
    () =>
      document.querySelector('video').currentTime >= 12 &&
      document.querySelector('video').readyState >= 2,
  );
  await page.locator('video').evaluate((video) => {
    video.currentTime = 3;
  });
  await page.getByRole('combobox', { name: 'Camera', exact: true }).click();
  await page.getByRole('option', { name: 'fcamera.hevc', exact: true }).click();
  await page.waitForFunction(() => {
    const video = document.querySelector('video');
    return (
      video.readyState >= 2 &&
      Math.abs(video.duration - 4) < 0.2 &&
      Math.abs(video.currentTime - 3) < 0.2
    );
  });
  await page.getByRole('combobox', { name: 'Camera', exact: true }).click();
  await page.getByRole('option', { name: 'qcamera.ts', exact: true }).click();
  await page.waitForFunction(() => {
    const video = document.querySelector('video');
    return (
      video.readyState >= 2 &&
      Math.abs(video.duration - 16) < 0.2 &&
      Math.abs(video.currentTime - 3) < 0.2
    );
  });
  await page.getByRole('heading', { name: 'Metadata', exact: true }).waitFor();
  await page.locator('.map-canvas').waitFor();
  await page.locator('.mapboxgl-canvas').first().waitFor({ state: 'attached' });
  assert.equal(
    await page.getByRole('button', { name: /Load map background/ }).count(),
    0,
  );
  assert.equal(
    await page.evaluate(
      () => document.documentElement.scrollHeight <= innerHeight,
    ),
    true,
  );
  assert.equal(
    await page
      .locator('.session-view')
      .evaluate((el) => el.scrollHeight <= el.clientHeight),
    true,
  );
  await page.screenshot({ path: '.artifacts/desktop.png', fullPage: true });
  await page.getByLabel('Session actions', { exact: true }).click();
  await page
    .getByRole('button', { name: 'Bookmark drive', exact: true })
    .click();
  await page.getByLabel('Session actions', { exact: true }).click();
  await page
    .getByRole('button', { name: 'Remove bookmark', exact: true })
    .waitFor();
  await page.getByLabel('Session actions', { exact: true }).click();
  await page.locator('.search input').fill('nonexistent');
  await page.getByRole('heading', { name: 'No matching drives' }).waitFor();
  await page.locator('.search input').fill('');
  await page.getByRole('button', { name: 'Overview', exact: true }).click();
  await page.getByRole('heading', { name: 'Recording overview' }).waitFor();
  assert.match(await page.locator('.overview').innerText(), /6 of 6 segments/);
  await page.screenshot({ path: '.artifacts/overview.png', fullPage: true });
  await page.getByRole('button', { name: 'Recordings', exact: true }).click();
  assert.equal(
    await page
      .getByRole('button', { name: 'Multiple feeds', exact: true })
      .count(),
    0,
  );
  assert.equal(await page.locator('video').count(), 1);
  const divider = page.getByRole('separator', { name: 'Resize video and map' });
  const originalWidth = (await page.locator('.session-video').boundingBox())
    .width;
  const originalDataWidth = (await page.locator('.session-data').boundingBox())
    .width;
  const handle = await divider.boundingBox();
  await page.mouse.move(
    handle.x + handle.width / 2,
    handle.y + handle.height / 2,
  );
  await page.mouse.down();
  await page.mouse.move(handle.x - 120, handle.y + handle.height / 2, {
    steps: 8,
  });
  await page.mouse.up();
  const resizedWidth = (await page.locator('.session-video').boundingBox())
    .width;
  assert(resizedWidth < originalWidth - 100);
  assert(
    (await page.locator('.session-data').boundingBox()).width >
      originalDataWidth + 100,
  );
  assert.equal(
    await page.evaluate(
      () =>
        document.documentElement.scrollHeight <= innerHeight &&
        document.documentElement.scrollWidth <= innerWidth,
    ),
    true,
  );
  await page.reload();
  await divider.waitFor();
  assert(
    Math.abs(
      (await page.locator('.session-video').boundingBox()).width - resizedWidth,
    ) < 2,
  );
  await divider.focus();
  await page.keyboard.press('ArrowRight');
  assert(
    (await page.locator('.session-video').boundingBox()).width >
      resizedWidth + 14,
  );
  await page.keyboard.press('End');
  assert((await page.locator('.session-data').boundingBox()).width >= 219);
  await page.keyboard.press('Home');
  assert((await page.locator('.session-video').boundingBox()).width >= 319);
  await divider.dblclick();
  assert(
    Math.abs(
      (await page.locator('.session-video').boundingBox()).width -
        originalWidth,
    ) < 2,
  );
  await page.screenshot({ path: '.artifacts/resizable-panels.png' });
  for (const format of ['zip', 'mp4', 'raw', 'mcap']) {
    await page.getByLabel('Session actions', { exact: true }).click();
    await page
      .getByRole('button', {
        name:
          format === 'raw'
            ? 'Download raw video'
            : `Download ${format.toUpperCase()}`,
        exact: true,
      })
      .click();
    assert.equal(
      await page
        .getByRole('combobox', { name: 'Format', exact: true })
        .innerText(),
      format === 'zip'
        ? 'ZIP · original cameras and logs'
        : format === 'mp4'
          ? 'MP4 · one continuous camera recording'
          : format === 'raw'
            ? 'Raw video · original camera parts (ZIP)'
            : 'MCAP · Dash telemetry + original attachments',
    );
    assert.equal(
      await page
        .getByRole('combobox', { name: /From segment|Through segment/ })
        .count(),
      0,
    );
    await page
      .getByRole('button', { name: 'Export recording', exact: true })
      .click();
    const link = page.getByRole('link', {
      name: `Download ${format.toUpperCase()}`,
    });
    await link.waitFor({ timeout: 30000 });
    const [download] = await Promise.all([
      page.waitForEvent('download'),
      link.click(),
    ]);
    await download.saveAs(`.artifacts/smoke.${format}`);
    if (format === 'raw')
      assert(download.suggestedFilename().endsWith('-raw-video.zip'));
    if (format === 'mp4') {
      await page.getByRole('combobox', { name: 'Format', exact: true }).click();
      await page
        .getByRole('option', {
          name: 'Raw video · original camera parts (ZIP)',
          exact: true,
        })
        .click();
      assert.equal(
        await page
          .getByRole('link', { name: 'Download RAW', exact: true })
          .count(),
        0,
        'Changing options must not relabel an old download',
      );
    }

    await page.getByRole('button', { name: 'Close export' }).click();
  }
  await page
    .getByRole('button', { name: 'Switch device', exact: true })
    .click();
  const [diagnostic] = await Promise.all([
    page.waitForEvent('download'),
    page.getByRole('button', { name: 'Download backend diagnostics' }).click(),
  ]);
  await diagnostic.saveAs('.artifacts/browser-diagnostics.txt');
  assert.match(diagnostic.suggestedFilename(), /^dash-diagnostics-.*\.txt$/);
  await page.screenshot({ path: '.artifacts/diagnostics.png', fullPage: true });
  await page.getByRole('button', { name: 'Close device settings' }).click();
  await page.getByRole('button', { name: 'Live', exact: true }).click();
  await page.getByRole('button', { name: 'Start live view' }).click();
  await page.getByText(/lacks managed local streaming/).waitFor();
  await setDriving('onroad');
  await page.getByText(/Index paused · on the road/).waitFor({ timeout: 8000 });
  assert.equal(
    await page.getByRole('button', { name: 'Start live view' }).isDisabled(),
    true,
  );
  const rejected = await page.request.post(`${origin}/api/exports`, {
    data: { drive: '2026-10-12--17-42-08', segments: [0], format: 'zip' },
  });
  assert.equal(rejected.status(), 409);
  await setDriving('parked');
  const mobile = await context.newPage();
  await mobile.setViewportSize({ width: 390, height: 844 });
  await mobile.goto(origin);
  await mobile.locator('.drive-row').first().click();
  await mobile.locator('.detail .mobile-back').waitFor();
  assert.equal(await mobile.locator('.sidebar').isVisible(), false);
  assert.equal(
    await mobile
      .getByRole('separator', { name: 'Resize video and map' })
      .isVisible(),
    false,
  );
  assert.equal(
    await mobile.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
    true,
  );
  assert.equal(
    await mobile.evaluate(
      () => document.documentElement.scrollHeight <= innerHeight,
    ),
    true,
  );
  assert.equal(
    await mobile
      .locator('.session-view')
      .evaluate((el) => el.scrollHeight <= el.clientHeight),
    true,
  );
  assert((await mobile.locator('.viewer-card').boundingBox()).height > 300);
  await mobile.getByRole('button', { name: 'Map & data', exact: true }).click();
  assert.equal(await mobile.locator('.session-data').isVisible(), true);
  assert.equal(
    await mobile.evaluate(
      () => document.documentElement.scrollHeight <= innerHeight,
    ),
    true,
  );
  await mobile.screenshot({
    path: '.artifacts/mobile-map.png',
    fullPage: true,
  });
  await mobile.getByRole('button', { name: 'Video', exact: true }).click();
  await mobile.screenshot({ path: '.artifacts/mobile.png', fullPage: true });
  await mobile.setViewportSize({ width: 844, height: 390 });
  assert.equal(
    await mobile.evaluate(
      () => document.documentElement.scrollHeight <= innerHeight,
    ),
    true,
  );
  await mobile.evaluate(
    () =>
      new Promise((resolve) =>
        requestAnimationFrame(() => requestAnimationFrame(resolve)),
      ),
  );
  assert(
    (await mobile.locator('.viewer-card .player').boundingBox()).height > 80,
  );
  await mobile.screenshot({ path: '.artifacts/landscape.png' });
  await mobile.setViewportSize({ width: 390, height: 844 });
  await mobile.locator('.detail .mobile-back').click();
  assert.equal(await mobile.locator('.sidebar').isVisible(), true);
  await mobile.getByRole('button', { name: 'Open navigation' }).click();
  await mobile
    .getByRole('dialog', { name: 'Navigation', exact: true })
    .waitFor();
  await mobile.getByRole('button', { name: 'Overview', exact: true }).click();
  await mobile
    .getByRole('heading', { name: 'Recording overview', exact: true })
    .waitFor();
  await mobile
    .getByRole('dialog', { name: 'Navigation', exact: true })
    .waitFor({ state: 'hidden' });

  await mobile.getByRole('button', { name: 'Open navigation' }).click();
  await mobile
    .getByRole('navigation', { name: 'Workspace' })
    .getByRole('button', { name: 'Recordings', exact: true })
    .click();
  assert.equal(await mobile.locator('.sidebar').isVisible(), true);
  await mobile.getByRole('button', { name: 'Open navigation' }).click();
  await mobile
    .getByRole('button', { name: 'Switch device', exact: true })
    .click();
  await mobile
    .getByRole('button', { name: 'Download backend diagnostics' })
    .waitFor();
  assert.equal(
    await mobile
      .getByRole('button', { name: 'Download backend diagnostics' })
      .isEnabled(),
    true,
  );
  await mobile.screenshot({
    path: '.artifacts/mobile-diagnostics.png',
    fullPage: true,
  });
  await mobile.getByRole('button', { name: 'Close device settings' }).click();
  await page.evaluate(async () => {
    await navigator.serviceWorker.ready;
  });
  await page.reload();
  await context.setOffline(true);
  await page.reload();
  await page.getByRole('link', { name: 'Dash home' }).waitFor();
  await context.setOffline(false);
  assert.deepEqual(errors, []);
  console.log(
    'Browser smoke passed: continuous playback/seeking across file boundaries, metadata/map, bookmarks/search, overview, single camera switching, persistent resizable panels, full-session ZIP/MP4/raw/MCAP downloads, diagnostic download, live unsupported state, onroad lockout, mobile navigation, offline PWA shell.',
  );
} finally {
  await setDriving(simulation ? 'unknown' : 'parked');
  await browser.close();
}
