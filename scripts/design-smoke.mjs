import { chromium } from '@playwright/test';
import assert from 'node:assert/strict';
const origin = process.env.DASH_TEST_URL || 'http://127.0.0.1:8088';
const browser = await chromium.launch({
  executablePath:
    process.env.CHROME_PATH ||
    '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
  headless: true,
});
try {
  const context = await browser.newContext({
    viewport: { width: 1440, height: 1080 },
  });
  const page = await context.newPage();
  const errors = [];
  page.on('pageerror', (e) => errors.push(e.message));
  await page.goto(origin);
  await page.getByRole('heading', { name: 'Your drives' }).waitFor();
  await page.locator('.map-canvas').waitFor(); // Cached GPS loads on first open, even with paused workers.
  await page.setViewportSize({ width: 1920, height: 1080 });
  assert(
    await page.evaluate(() =>
      ['.topbar', '.workspace', '.detail'].every(
        (selector) =>
          Math.abs(
            document.querySelector(selector).getBoundingClientRect().right -
              innerWidth,
          ) < 1,
      ),
    ),
    'Wide windows must fill the right edge without an artificial margin',
  );

  for (const [width, height] of [
    [1440, 1080],
    [1024, 768],
    [936, 600],
    [390, 844],
    [844, 390],
  ]) {
    await page.setViewportSize({ width, height });
    if (
      width <= 900 &&
      !(await page
        .getByRole('button', { name: 'Video', exact: true })
        .isVisible())
    )
      await page.locator('.drive-row').first().click();
    await page.evaluate(
      () =>
        new Promise((resolve) =>
          requestAnimationFrame(() => requestAnimationFrame(resolve)),
        ),
    );
    assert(
      await page.evaluate(
        () =>
          document.documentElement.scrollHeight <= innerHeight &&
          document.documentElement.scrollWidth <= innerWidth,
      ),
      `Viewport overflow at ${width}x${height}`,
    );
    assert(
      await page
        .locator('.viewer-toolbar')
        .evaluate((e) => e.scrollWidth <= e.clientWidth),
      `Camera controls overflow at ${width}x${height}`,
    );
  }
  await page.setViewportSize({ width: 390, height: 844 });
  await page.getByRole('button', { name: 'Open navigation' }).click();
  const drawer = page.getByRole('dialog', { name: 'Navigation', exact: true });
  await drawer.waitFor();
  await page.keyboard.press('Tab');
  assert(
    await drawer.evaluate((e) => e.contains(document.activeElement)),
    'Drawer must retain keyboard focus',
  );
  await page.keyboard.press('Escape');
  await drawer.waitFor({ state: 'hidden' });
  await page.getByRole('button', { name: 'Open navigation' }).click();
  await drawer.waitFor();
  await drawer.evaluate((element) =>
    Promise.all(
      element
        .getAnimations()
        .map((animation) => animation.finished.catch(() => {})),
    ),
  );
  await page.screenshot({ path: '.artifacts/glass-drawer.png' });
  await page.keyboard.press('Escape');
  await drawer.waitFor({ state: 'hidden' });
  await page.getByRole('button', { name: 'Open navigation' }).focus();
  await page.keyboard.press('Enter');
  await drawer.waitFor();
  assert(
    await drawer.evaluate((element) => element.getAnimations().length === 0),
    'Keyboard navigation must remain immediate',
  );
  await page.keyboard.press('Escape');
  await drawer.waitFor({ state: 'hidden' });
  const client = await context.newCDPSession(page);
  await client.send('Emulation.setEmulatedMedia', {
    features: [
      { name: 'prefers-reduced-motion', value: 'reduce' },
      { name: 'prefers-reduced-transparency', value: 'reduce' },
      { name: 'prefers-contrast', value: 'more' },
    ],
  });
  await page.getByRole('button', { name: 'Open navigation' }).click();
  await drawer.waitFor();
  assert(
    await drawer.evaluate(
      (e) =>
        getComputedStyle(e).backdropFilter === 'none' &&
        e.getAnimations().length === 0,
    ),
    'Accessible material must remove blur and motion',
  );
  assert(
    await page
      .locator('.session-mobile-tabs')
      .evaluate(
        (e) => getComputedStyle(e, '::before').transitionDuration === '0s',
      ),
    'Reduced motion must stop selection morphs',
  );
  await page.keyboard.press('Escape');
  await drawer.waitFor({ state: 'hidden' });
  await page.getByRole('button', { name: 'Map & data', exact: true }).click();
  await page.screenshot({ path: '.artifacts/glass-accessibility.png' });
  const phoneContext = await browser.newContext({
    viewport: { width: 390, height: 844 },
  });
  const phone = await phoneContext.newPage();
  phone.on('pageerror', (e) => errors.push(e.message));
  await phone.goto(origin);
  await phone.getByRole('heading', { name: 'Your drives' }).waitFor();
  await phone.locator('.drive-row').first().click();
  await phone.getByRole('button', { name: 'Map & data', exact: true }).click();
  await phone.locator('.map-canvas').waitFor();
  await phone.screenshot({ path: '.artifacts/fresh-phone-map.png' });
  await phoneContext.close();
  assert.deepEqual(errors, []);
  console.log(
    'Design smoke passed: five viewport sizes, camera control fit, drawer focus/Escape/reopen, reduced motion/transparency and high contrast.',
  );
} finally {
  await browser.close();
}
