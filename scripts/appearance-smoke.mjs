import { chromium } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
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
    viewport: { width: 1440, height: 900 },
    colorScheme: 'light',
  });
  const page = await context.newPage();
  const errors = [];
  page.on('pageerror', (e) => errors.push(e.message));
  await page.goto(origin);
  await page.getByRole('heading', { name: 'Your drives' }).waitFor();
  async function settings() {
    await page.getByRole('button', { name: 'Settings', exact: true }).click();
  }
  async function closeSettings() {
    await page.getByRole('button', { name: 'Close settings' }).click();
    await page
      .getByRole('dialog', { name: 'Settings', exact: true })
      .waitFor({ state: 'hidden' });
  }
  async function audit(name) {
    console.log(`Auditing ${name}`);
    // Audit the resting surface rather than sampling a dialog's entrance fade.
    await page.evaluate(
      () =>
        new Promise((resolve) =>
          requestAnimationFrame(() => requestAnimationFrame(resolve)),
        ),
    );
    await page.waitForFunction(() =>
      [
        ...document.querySelectorAll(
          'dialog[open], .scrim, [role="dialog"], .session-action-menu:popover-open, .menulist:popover-open',
        ),
      ].every((element) => Number(getComputedStyle(element).opacity) >= 0.999),
    );
    const result = await new AxeBuilder({ page })
      .withRules(['color-contrast'])
      .analyze();
    assert.deepEqual(
      result.violations.map((v) => ({
        id: v.id,
        nodes: v.nodes.map((n) => ({
          target: n.target,
          reason: n.failureSummary,
        })),
      })),
      [],
      `Contrast failures: ${name}`,
    );
  }
  for (const theme of ['Light', 'Dark'])
    for (const performance of [false, true]) {
      await settings();
      await page.getByRole('radio', { name: theme, exact: true }).check();
      await page
        .getByRole('switch', { name: 'Performance mode' })
        .setChecked(performance);
      await audit(`${theme} settings / performance ${performance}`);
      if (performance) {
        assert(
          await page
            .locator('.settings-popup')
            .evaluate(
              (e) =>
                getComputedStyle(e).backdropFilter === 'none' &&
                !getComputedStyle(e).backgroundColor.endsWith(', 0.93)'),
            ),
        );
      }
      await page.screenshot({
        path: `.artifacts/settings-${theme.toLowerCase()}-${performance}.png`,
      });
      await closeSettings();
      if (performance)
        assert(
          await page.evaluate(() =>
            [
              ...document.querySelectorAll(
                '.topbar, .viewer-toolbar, .map-toolbar, .session-data',
              ),
            ].every(
              (e) =>
                getComputedStyle(e).backdropFilter === 'none' &&
                !getComputedStyle(e).backgroundColor.startsWith('rgba'),
            ),
          ),
        );
      assert(
        await page
          .locator('.detail')
          .evaluate((e) => getComputedStyle(e).borderTopLeftRadius === '0px'),
      );
      assert(
        await page
          .locator('.session-data')
          .evaluate((e) => getComputedStyle(e).borderTopLeftRadius === '0px'),
      );
      if (theme === 'Light')
        assert(
          await page.evaluate(
            () =>
              getComputedStyle(document.querySelector('.detail'))
                .backgroundColor !==
              getComputedStyle(document.querySelector('.sidebar'))
                .backgroundColor,
          ),
          'Light sidebar and content must use distinct surfaces',
        );
      const stable = await page.locator('.sidebar').boundingBox();
      assert.equal(stable.x, 0);
      assert.equal(await page.locator('.topbar').isVisible(), false);
      for (const view of ['Overview', 'Live', 'Recordings']) {
        await page.getByRole('button', { name: view, exact: true }).click();
        const bounds = await page.locator('.sidebar').boundingBox();
        assert.equal(bounds.x, stable.x);
        assert.equal(bounds.width, stable.width);
        assert.equal(bounds.height, stable.height);
        await audit(`${theme} ${view} / performance ${performance}`);
      }
      await page.getByLabel('Session actions', { exact: true }).click();
      await audit(`${theme} actions`);
      await page
        .getByRole('button', { name: 'Original files', exact: true })
        .click();
      await audit(`${theme} files`);
      await page.getByRole('button', { name: 'Close original files' }).click();
      await page.getByLabel('Session actions', { exact: true }).click();
      await page
        .getByRole('button', { name: 'Download MP4', exact: true })
        .click();
      await audit(`${theme} export`);
      await page.getByRole('button', { name: 'Close export' }).click();
      await page
        .getByRole('button', { name: 'Switch device', exact: true })
        .click();
      await audit(`${theme} device`);
      await page.getByRole('button', { name: 'Close device settings' }).click();
      await page.getByRole('button', { name: 'Help and installation' }).click();
      await audit(`${theme} help`);
      await page.getByRole('button', { name: 'Close help' }).click();
    }
  await page.reload();
  await page.getByRole('heading', { name: 'Your drives' }).waitFor();
  assert(
    await page.evaluate(
      () =>
        document.documentElement.dataset.theme === 'dark' &&
        document.documentElement.dataset.performance === 'true',
    ),
  );
  await settings();
  await page.getByRole('radio', { name: 'System', exact: true }).check();
  await closeSettings();
  await page.emulateMedia({ colorScheme: 'light' });
  await page.waitForFunction(
    () => document.documentElement.dataset.theme === 'light',
  );
  await page.emulateMedia({ colorScheme: 'dark' });
  await page.waitForFunction(
    () => document.documentElement.dataset.theme === 'dark',
  );
  await settings();
  await page.getByRole('radio', { name: 'Light', exact: true }).check();
  await closeSettings();
  await page.emulateMedia({ colorScheme: 'dark' });
  assert.equal(
    await page.evaluate(() => document.documentElement.dataset.theme),
    'light',
  );
  const second = await context.newPage();
  await second.goto(origin);
  await second.getByRole('heading', { name: 'Your drives' }).waitFor();
  await settings();
  await page.getByRole('radio', { name: 'Dark', exact: true }).check();
  await closeSettings();
  await second.waitForFunction(
    () => document.documentElement.dataset.theme === 'dark',
  );
  await second.close();
  await page.setViewportSize({ width: 390, height: 844 });
  await page.locator('.drive-row').first().click();
  const bounds = await page.locator('.session-mobile-tabs').boundingBox();
  await page.getByRole('button', { name: 'Map & data', exact: true }).click();
  assert.deepEqual(
    await page.locator('.session-mobile-tabs').boundingBox(),
    bounds,
  );
  await audit('Mobile map / dark performance');
  await page.getByRole('button', { name: 'Open navigation' }).click();
  await audit('Mobile drawer / dark performance');
  await page.keyboard.press('Escape');
  await page
    .getByRole('dialog', { name: 'Navigation', exact: true })
    .waitFor({ state: 'hidden' });
  await settings();
  await audit('Mobile settings / dark performance');
  await closeSettings();
  assert.deepEqual(errors, []);
  console.log(
    'Appearance smoke passed: light/dark contrast audits across settings, viewer, overview, live, actions, files, export, device, help and mobile; opaque/no-blur performance mode; persistence, live System theme, cross-tab sync and stable window geometry across tabs.',
  );
} finally {
  await browser.close();
}
