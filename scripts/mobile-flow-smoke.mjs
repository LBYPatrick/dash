import { chromium } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import assert from 'node:assert/strict';
import { installMapFixture } from './map-fixture.mjs';
const origin = process.env.DASH_TEST_URL || 'http://127.0.0.1:8088';
const browser = await chromium.launch({
  executablePath:
    process.env.CHROME_PATH ||
    '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
  headless: true,
});
const context = await browser.newContext({
  viewport: { width: 390, height: 844 },
  hasTouch: true,
  isMobile: true,
});
await installMapFixture(context);
const errors = [];
try {
  const page = await context.newPage();
  page.on('pageerror', (e) => errors.push(e.message));
  await page.goto(origin);
  await page.locator('.drive-row').first().waitFor();
  const navigate = async (name) => {
    await page
      .getByRole('button', { name: 'Open navigation', exact: true })
      .click();
    await page
      .getByRole('dialog', { name: 'Navigation', exact: true })
      .getByRole('button', { name, exact: true })
      .click();
  };
  await navigate('Overview');
  await page.getByRole('heading', { name: 'Recording overview' }).waitFor();
  await page
    .getByRole('button', { name: 'Recorded time', exact: false })
    .first()
    .click();
  await page.locator('.chart-table summary').click();
  await page.locator('.detail').evaluate((e) => (e.scrollTop = 300));
  await page.locator('.dashboard-drive').first().scrollIntoViewIfNeeded();
  const scroll = await page.locator('.detail').evaluate((e) => e.scrollTop);
  await page.locator('.dashboard-drive').first().click();
  await page
    .getByRole('button', { name: 'Back to Overview', exact: true })
    .waitFor();
  await page
    .getByRole('button', { name: 'Back to Overview', exact: true })
    .click();
  await page.getByRole('heading', { name: 'Recording overview' }).waitFor();
  assert(
    await page.locator('.chart-table').evaluate((e) => e.open),
    'Chart disclosure state survives returning',
  );
  assert(
    Math.abs(
      (await page.locator('.detail').evaluate((e) => e.scrollTop)) - scroll,
    ) < 3,
    'Overview scroll survives returning',
  );
  await page.locator('.dashboard-drive').first().click();
  await page.goBack();
  await page.getByRole('heading', { name: 'Recording overview' }).waitFor();
  await navigate('Recordings');
  await page.locator('.drive-row').first().click();
  await page
    .locator('video')
    .evaluate((v) =>
      v.readyState >= 1
        ? Promise.resolve()
        : new Promise((r) =>
            v.addEventListener('loadedmetadata', r, { once: true }),
          ),
    );
  await page.getByRole('button', { name: 'Map & data', exact: true }).click();
  assert.equal(
    await page
      .getByRole('button', { name: 'Map & data', exact: true })
      .getAttribute('aria-pressed'),
    'true',
  );
  await page
    .getByRole('button', { name: 'Play recording', exact: true })
    .click();
  await page.waitForTimeout(550);
  assert(
    await page.locator('video').evaluate((v) => v.currentTime > 0 && !v.paused),
    'Map view retains actual playback',
  );
  await page
    .getByRole('button', { name: 'Pause recording', exact: true })
    .click();
  await page
    .getByRole('button', { name: 'Enter playback timestamp', exact: true })
    .click();
  await page
    .getByRole('textbox', { name: 'Playback timestamp', exact: true })
    .fill('0:03.125');
  await page.getByRole('button', { name: 'Go', exact: true }).click();
  assert(
    Math.abs(
      (await page.locator('video').evaluate((v) => v.currentTime)) - 3.125,
    ) < 0.05,
    'Map timestamp seeks same video',
  );
  await page.getByRole('button', { name: 'Trip details', exact: true }).click();
  const details = page.getByRole('dialog', {
    name: 'Trip details',
    exact: true,
  });
  await details.waitFor();
  assert(await details.getByText('Vehicle', { exact: true }).isVisible());
  await page
    .getByRole('button', { name: 'Close trip details', exact: true })
    .click();
  for (const [width, height] of [
    [390, 844],
    [320, 568],
    [844, 390],
    [667, 375],
  ]) {
    await page.setViewportSize({ width, height });
    await page.waitForTimeout(250);
    assert(
      await page
        .getByRole('button', { name: 'All drives', exact: true })
        .isVisible(),
      'Back remains visible in landscape',
    );
    assert(
      await page
        .getByRole('button', { name: 'Fullscreen recording', exact: true })
        .isVisible(),
      'Supported mobile fullscreen remains discoverable',
    );
    const bad = await page
      .locator(
        '.mobile-playback-dock button,.mobile-playback-dock input[type=range],.session-mobile-tabs button,.map-toolbar button,.session-return',
      )
      .evaluateAll((es) =>
        es
          .filter((e) => e.getBoundingClientRect().width > 0)
          .filter(
            (e) =>
              e.getBoundingClientRect().height < 43.5 ||
              e.getBoundingClientRect().width < 43.5,
          )
          .map((e) => ({
            name: e.getAttribute('aria-label') || e.textContent.trim(),
            rect: e.getBoundingClientRect().toJSON(),
          })),
      );
    assert.deepEqual(bad, [], `Touch targets at ${width}x${height}`);
    const layout = await page.locator('.session-view').evaluate((e) => ({
      width: e.clientWidth,
      scrollWidth: e.scrollWidth,
      height: e.clientHeight,
      scrollHeight: e.scrollHeight,
      document: document.documentElement.scrollHeight,
      viewport: innerHeight,
      map: e.querySelector('.map-canvas')?.getBoundingClientRect().height,
    }));
    assert(
      layout.scrollWidth <= layout.width + 1 &&
        layout.scrollHeight <= layout.height + 1 &&
        layout.document <= layout.viewport,
      `Fixed mobile viewer at ${width}x${height}: ${JSON.stringify(layout)}`,
    );
    assert(
      layout.map > 90,
      `Map must retain useful height: ${JSON.stringify(layout)}`,
    );
    await page.screenshot({
      path: `.artifacts/mobile-flow-${width}x${height}.png`,
    });
  }
  await page.setViewportSize({ width: 390, height: 844 });
  // Standard fullscreen from map reveals the picture and brings its one set of
  // controls into fullscreen, then returns them to the shared mobile dock.
  await page
    .getByRole('button', { name: 'Fullscreen recording', exact: true })
    .click();
  await page.waitForFunction(() => !!document.fullscreenElement);
  assert.equal(
    await page
      .getByRole('button', { name: 'Video', exact: true })
      .getAttribute('aria-pressed'),
    'true',
  );
  await page
    .getByRole('button', { name: 'Exit fullscreen recording', exact: true })
    .click();
  await page.waitForFunction(() => !document.fullscreenElement);
  await page
    .locator('.mobile-playback-dock .enhanced-controls')
    .waitFor({ state: 'visible' });
  for (const theme of ['Light', 'Dark']) {
    // Exercise the public preference so palette variables and theme selectors
    // change together; changing only data-theme creates an impossible palette.
    await page.getByRole('button', { name: 'Settings', exact: true }).click();
    await page.getByRole('radio', { name: theme, exact: true }).check();
    await page
      .getByRole('button', { name: 'Close settings', exact: true })
      .click();
    await page
      .getByRole('dialog', { name: 'Settings', exact: true })
      .waitFor({ state: 'hidden' });
    const result = await new AxeBuilder({ page })
      .withTags(['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'])
      .analyze();
    assert.deepEqual(
      result.violations.map((v) => ({
        id: v.id,
        nodes: v.nodes.map((n) => n.target),
      })),
      [],
    );
  }
  await page.getByRole('button', { name: 'All drives', exact: true }).click();
  await page.locator('.drive-row').first().waitFor({ state: 'visible' });
  await page.locator('.drive-row').first().click();
  await page.reload();
  await page.getByRole('button', { name: 'All drives', exact: true }).waitFor();
  assert(
    await page.getByRole('button', { name: 'Video', exact: true }).isVisible(),
    'Reload restores active mobile session from browser history',
  );
  assert.deepEqual(errors, []);
  console.log(
    'Mobile flows passed: contextual return/browser Back, retained Overview state/scroll, shared map playback and timestamp seeking, Trip details, supported fullscreen, reload restoration, 44px targets, fixed portrait/small/landscape layouts, useful map height, light/dark accessibility. Physical Safari/keyboard/notch verification remains outstanding.',
  );
} finally {
  await context.close();
  await browser.close();
}
