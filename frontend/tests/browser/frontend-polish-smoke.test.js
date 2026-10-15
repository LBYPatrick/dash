import { chromium, expect } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import { assert, test } from 'vite-plus/test';
import { installMapFixture } from './map-fixture.js';

test('frontend-polish-smoke', async () => {
  // Browser-only UI checks; use the local synthetic library, never a car.

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
    const page = await context.newPage();
    const errors = [];
    page.on('pageerror', (error) => errors.push(error.message));
    await page.goto(origin);
    await page.locator('.drive-row').first().waitFor();
    const settings = () =>
      page.getByRole('button', { name: 'Settings', exact: true });
    async function audit(name) {
      await page.waitForFunction(() =>
        [...document.querySelectorAll('.scrim, :popover-open')].every(
          (e) => Number(getComputedStyle(e).opacity) >= 0.999,
        ),
      );
      const result = await new AxeBuilder({ page })
        .withTags(['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'])
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
        name,
      );
    }
    // All accent/theme pairs, including controls on raised surfaces.
    for (const theme of ['Light', 'Dark']) {
      await settings().click();
      await page.getByRole('radio', { name: theme, exact: true }).check();
      for (const accent of [
        'Blue',
        'Purple',
        'Pink',
        'Red',
        'Orange',
        'Yellow',
        'Green',
        'Graphite',
      ]) {
        await page.getByRole('button', { name: accent, exact: true }).click();
        await audit(`${theme}/${accent} settings`);
      }
      await page.getByRole('button', { name: 'Blue', exact: true }).click();
      const parent = page.getByRole('dialog', {
        name: 'Settings',
        exact: true,
      });
      assert(
        await page
          .locator('.workspace')
          .evaluate((e) => !!e.closest('[inert]')),
      );
      await parent.getByRole('button', { name: /Dash updates/ }).click();
      const updates = page.getByRole('dialog', {
        name: 'Dash updates',
        exact: true,
      });
      await expect(updates).toBeVisible();
      assert(
        await page
          .locator('.settings-popup')
          .first()
          .evaluate((e) => !!e.closest('[inert]')),
        'Parent sheet is isolated',
      );
      await audit(`${theme} nested updates`);
      await page.keyboard.press('Escape');
      await expect(parent).toBeVisible();
      await expect(
        parent.getByRole('button', { name: /Dash updates/ }),
      ).toBeFocused();
      await page.keyboard.press('Escape');
      await expect(settings()).toBeFocused();
      assert(
        await page.locator('.workspace').evaluate((e) => !e.closest('[inert]')),
      );
      for (const view of ['Overview', 'Live', 'Recordings']) {
        await page.getByRole('button', { name: view, exact: true }).click();
        await audit(`${theme} ${view}`);
      }
    }
    const actions = page.getByRole('button', {
      name: 'Session actions',
      exact: true,
    });
    await actions.focus();
    await page.keyboard.press('ArrowUp');
    const actionGroup = page.getByRole('group', {
      name: 'Session actions',
      exact: true,
    });
    await expect(actionGroup.getByRole('button').last()).toBeFocused();
    await page.keyboard.press('Home');
    await expect(actionGroup.getByRole('button').first()).toBeFocused();
    await page.keyboard.press('Escape');
    await expect(actions).toBeFocused();
    const follow = page.getByRole('button', { name: 'Follow', exact: true });
    await expect(follow).toBeEnabled();
    await follow.click();
    await page.locator('.map-canvas canvas').focus();
    await page.keyboard.press('Tab');
    await expect(follow).toHaveAttribute('aria-pressed', 'true');
    // Keyboard focus has a solid tonal marker rather than a decorative outline.
    await settings().focus();
    await page.keyboard.press('Tab');
    assert(
      await page.evaluate(
        () => getComputedStyle(document.activeElement).boxShadow !== 'none',
      ),
    );

    for (const locale of ['en-US', 'ja', 'zh-CN', 'ko', 'vi']) {
      await page.evaluate(
        (locale) =>
          localStorage.setItem('dash.language', JSON.stringify(locale)),
        locale,
      );
      await page.reload();
      await page.setViewportSize({ width: 390, height: 844 });
      if (await page.locator('.session-return').isVisible())
        await page.locator('.session-return').click();
      for (const [width, height] of [
        [320, 480],
        [390, 844],
        [667, 375],
        [844, 390],
      ]) {
        await page.setViewportSize({ width, height });
        const row = page.locator('.drive-row').first();
        if (await row.isVisible()) {
          await row.focus();
          await page.keyboard.press('Enter');
          await expect(page.locator('.detail h1')).toBeFocused();
        }
        await page.waitForFunction(
          () => document.querySelector('video')?.readyState >= 1,
        );
        await page.locator('.timestamp-button').click();
        await expect(page.locator('.timestamp-form input')).toBeFocused();
        const bad = await page
          .locator('.session-view button, .session-view input')
          .evaluateAll((elements) =>
            elements
              .filter(
                (e) =>
                  e.checkVisibility() &&
                  !e.closest('[popover]:not(:popover-open)'),
              )
              .map((e) => ({
                name: e.getAttribute('aria-label') || e.textContent,
                r: e.getBoundingClientRect(),
              }))
              .filter(
                ({ r }) =>
                  r.left < -0.5 ||
                  r.right > innerWidth + 0.5 ||
                  r.top < -0.5 ||
                  r.bottom > innerHeight + 0.5,
              ),
          );
        assert.deepEqual(
          bad,
          [],
          `Visible controls fit ${locale} ${width}x${height}`,
        );
        assert(
          await page
            .locator('.session-view')
            .evaluate(
              (e) =>
                e.scrollWidth <= e.clientWidth + 1 &&
                e.scrollHeight <= e.clientHeight + 1,
            ),
          'Viewer stays fixed',
        );
        await page.keyboard.press('Escape');
        await audit(`${locale} ${width}x${height}`);
      }
      await page.locator('.session-return').click();
      await expect(page.locator('.drive-row').first()).toBeFocused();
    }
    assert.deepEqual(errors, []);
    console.log(
      'Frontend polish passed: 16 accent/theme combinations, nested modal isolation/focus return, action keys, map Follow on Tab, and 20 localized fixed mobile layouts/timestamp forms.',
    );
  } finally {
    await browser.close();
  }
});
