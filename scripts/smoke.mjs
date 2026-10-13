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
  await page.waitForFunction(
    () => document.querySelector('video')?.readyState >= 1,
  );
  await page.getByRole('heading', { name: 'Metadata', exact: true }).waitFor();
  await page.locator('.map-canvas').waitFor();
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
  await page
    .getByRole('button', { name: 'Multiple feeds', exact: true })
    .click();
  await page.getByLabel('H.264 compatibility').check();
  await page.getByRole('button', { name: 'Load selected feeds' }).click();
  await page.waitForFunction(
    () => {
      const videos = [...document.querySelectorAll('.feed-grid video')];
      return (
        videos.length === 2 && videos.every((video) => video.readyState >= 1)
      );
    },
    { timeout: 60000 },
  );
  await page.getByRole('button', { name: 'Play feeds together' }).click();
  await page.waitForFunction(() =>
    [...document.querySelectorAll('.feed-grid video')].every(
      (video) => video.currentTime > 0,
    ),
  );
  await page.getByRole('button', { name: 'Pause feeds together' }).click();
  await page.screenshot({
    path: '.artifacts/multiple-feeds.png',
    fullPage: true,
  });
  for (const format of ['zip', 'mp4', 'mcap']) {
    await page.getByLabel('Session actions', { exact: true }).click();
    await page
      .getByRole('button', {
        name: `Download ${format.toUpperCase()}`,
        exact: true,
      })
      .click();
    assert.equal(
      await page.getByLabel('Format', { exact: true }).inputValue(),
      format,
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
    await page.getByRole('button', { name: 'Close export' }).click();
  }
  await page.locator('.device-card').click();
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
  await mobile.locator('.device-card').click();
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
    'Browser smoke passed: playback, metadata/map, bookmarks/search, overview, two feeds, ZIP/MP4/MCAP downloads, diagnostic download, live unsupported state, onroad lockout, mobile navigation, offline PWA shell.',
  );
} finally {
  await setDriving(simulation ? 'unknown' : 'parked');
  await browser.close();
}
