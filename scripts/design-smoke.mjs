import { installMapFixture } from './map-fixture.mjs';
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
  await installMapFixture(context);
  const page = await context.newPage();
  const errors = [];
  page.on('pageerror', (e) => errors.push(e.message));
  await page.goto(origin);
  await page.getByRole('heading', { name: 'Your drives' }).waitFor();
  await page.locator('.map-canvas').waitFor(); // Cached GPS loads on first open, even with paused workers.
  await page.setViewportSize({ width: 1920, height: 1080 });
  assert(
    await page.evaluate(() =>
      ['.workspace', '.detail'].every(
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
    if (width > 900) {
      assert.equal(await page.locator('.topbar').isVisible(), false);
      assert(await page.locator('.desktop-sidebar-nav').isVisible());
      assert.equal(
        await page
          .locator('.desktop-sidebar-nav .workspace-nav button')
          .count(),
        3,
      );
    } else {
      assert(
        await page.locator('.topbar').evaluate((bar) => {
          const bounds = bar
            .querySelector('.mobile-page-title')
            .getBoundingClientRect();
          return Math.abs(bounds.x + bounds.width / 2 - innerWidth / 2) < 1;
        }),
        `Mobile destination must remain centered at ${width}px`,
      );
    }
    assert(
      await page
        .locator(width > 900 ? '.sidebar' : '.topbar')
        .evaluate((surface) =>
          [...surface.querySelectorAll('button, a')]
            .filter((e) => e.getBoundingClientRect().width > 0)
            .every((e) => {
              const rect = e.getBoundingClientRect();
              return (
                rect.left >= 0 && rect.right <= innerWidth && rect.height >= 26
              );
            }),
        ),
      `Navigation controls must fit at ${width}px`,
    );
    assert(
      await page
        .locator('.viewer-toolbar')
        .evaluate((e) => e.scrollWidth <= e.clientWidth),
      `Camera controls overflow at ${width}x${height}`,
    );
  }
  await page.setViewportSize({ width: 936, height: 600 });
  await page
    .locator('.sidebar-device span')
    .evaluate(
      (e) =>
        (e.textContent =
          'a-very-long-device-name-on-the-local-network.example'),
    );
  assert(
    await page
      .locator('.desktop-sidebar-nav')
      .evaluate((e) => e.scrollWidth <= e.clientWidth),
    'Long device names must truncate within the sidebar',
  );
  await page.screenshot({ path: '.artifacts/sidebar-desktop.png' });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.screenshot({ path: '.artifacts/toolbar-mobile.png' });
  await page.getByRole('button', { name: 'Open navigation' }).click();
  const drawer = page.getByRole('dialog', { name: 'Navigation', exact: true });
  await drawer.waitFor();
  await drawer.getByRole('button', { name: 'Help and installation' }).click();
  await page.getByRole('dialog', { name: 'Help', exact: true }).waitFor();
  await page.getByRole('button', { name: 'Close help' }).click();
  await page
    .getByRole('dialog', { name: 'Help', exact: true })
    .waitFor({ state: 'hidden' });
  assert(
    await page
      .getByRole('button', { name: 'Open navigation' })
      .evaluate((e) => e === document.activeElement),
    'Help must return focus to the visible navigation button',
  );
  await page.getByRole('button', { name: 'Open navigation' }).click();
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
  await installMapFixture(phoneContext);
  const phone = await phoneContext.newPage();
  phone.on('pageerror', (e) => errors.push(e.message));
  await phone.goto(origin);
  await phone.getByRole('heading', { name: 'Your drives' }).waitFor();
  await phone.locator('.drive-row').first().click();
  await phone.getByRole('button', { name: 'Map & data', exact: true }).click();
  await phone.locator('.map-canvas').waitFor();
  await phone.screenshot({ path: '.artifacts/fresh-phone-map.png' });
  await phone.route('**/api/**', (route) => route.abort());
  await phone.route('**/files/**', (route) => route.abort());
  await phone.reload();
  await phone
    .getByRole('heading', { name: 'Review your recordings.' })
    .waitFor();
  assert(
    await phone
      .getByRole('button', { name: 'Help and installation' })
      .isVisible(),
    'Installation help must remain reachable before a device connects',
  );
  await phone.getByRole('button', { name: 'Help and installation' }).click();
  await phone.getByRole('dialog', { name: 'Help', exact: true }).waitFor();
  await phoneContext.close();
  assert.deepEqual(errors, []);
  console.log(
    'Design smoke passed: five viewport sizes, camera control fit, drawer focus/Escape/reopen, reduced motion/transparency and high contrast.',
  );
} finally {
  await browser.close();
}
