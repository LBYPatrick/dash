import { chromium } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import { assert, test } from 'vite-plus/test';
import { installMapFixture } from './map-fixture.js';

test('updates-smoke', async () => {
  const origin = process.env.DASH_TEST_URL || 'http://127.0.0.1:8088';
  const browser = await chromium.launch({
    executablePath:
      process.env.CHROME_PATH ||
      '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
    headless: true,
    args: [
      '--disable-gpu',
      '--disable-accelerated-video-decode',
      '--use-angle=swiftshader',
    ],
  });
  try {
    const context = await browser.newContext({
      viewport: { width: 1440, height: 900 },
    });
    await installMapFixture(context);
    const current = {
      version: '0.1.0+main.10.aaaaaaaa',
      tag: 'v0.1.0+main.10.aaaaaaaa',
      channel: 'main',
      revision: 'a'.repeat(40),
    };
    const state = {
      managed: true,
      repository: 'LBYPatrick/dash',
      current,
      channel: 'main',
      automatic: true,
      phase: 'idle',
    };
    const actions = [];
    let healthFinished = false,
      driving = 'parked',
      initialLoadFails = true;
    if (process.env.DASH_TEST_FIXTURE) {
      await context.route('**/api/info', (route) =>
        route.fulfill({ json: { kind: 'dash-backend', version: 1 } }),
      );
      await context.route('**/files/**', (route) =>
        route.fulfill({
          json: {
            dirs: [{ href: '/files/2026-10-14--09-00-00--0/' }],
            files: [],
          },
        }),
      );
    }
    await context.route('**/api/index', async (route) => {
      if (process.env.DASH_TEST_FIXTURE) {
        await route.fulfill({
          json: {
            version: 1,
            status: {
              running: false,
              completed: 0,
              total: 1,
              errors: 0,
              driving,
            },
            segments: {},
          },
        });
      } else {
        const response = await route.fetch();
        const data = await response.json();
        data.status.driving = driving;
        await route.fulfill({ response, json: data });
      }
    });
    await context.route('**/api/updates', async (route) => {
      if (route.request().method() === 'GET' && initialLoadFails) {
        initialLoadFails = false;
        await route.fulfill({
          status: 503,
          json: { error: 'temporary unavailable' },
        });
        return;
      }
      if (route.request().method() === 'POST') {
        const input = route.request().postDataJSON();
        actions.push(input.action);
        if (input.action === 'configure') {
          state.channel = input.channel;
          state.automatic = input.automatic;
          state.phase = 'idle';
        }
        if (input.action === 'check') {
          state.phase = 'available';
          state.release = {
            version: '0.1.0-nightly.11.bbbbbbbb',
            tag: 'v0.1.0-nightly.11.bbbbbbbb',
            channel: 'nightly',
            revision: 'b'.repeat(40),
          };
        }
        if (input.action === 'download') {
          state.phase = 'downloading';
          state.total = 100;
          state.transferred = 30;
          setTimeout(() => {
            state.phase = 'ready';
            state.message = 'Download verified.';
          }, 300);
        }
        if (input.action === 'install') {
          state.phase = 'restarting';
          state.pendingHealth = true;
          setTimeout(() => {
            state.current = state.release;
          }, 300);
          setTimeout(() => {
            state.pendingHealth = false;
            state.phase = 'idle';
            state.message = '';
            delete state.release;
            healthFinished = true;
          }, 2500);
        }
      }
      await route.fulfill({ json: state });
    });
    const page = await context.newPage();
    await page.goto(origin);
    await page.locator('.drive-row').first().waitFor();
    const open = async () => {
      await page.getByRole('button', { name: 'Settings', exact: true }).click();
      await page
        .getByRole('button', { name: 'Dash updates', exact: true })
        .click();
    };
    await open();
    await page
      .getByText('Update information is unavailable.', { exact: true })
      .waitFor();
    await page
      .getByRole('button', { name: 'Check for updates', exact: true })
      .click();
    await page
      .getByText('Update information is unavailable.', { exact: true })
      .waitFor({ state: 'hidden' });
    await page
      .getByRole('combobox', { name: 'Delivery channel', exact: true })
      .click();
    for (const name of ['Main · stable', 'Nightly', 'Dev']) {
      assert(await page.getByRole('option', { name, exact: true }).isVisible());
    }
    await page.getByRole('option', { name: 'Nightly', exact: true }).click();
    await Promise.all([
      page.waitForResponse(
        (r) =>
          r.url().endsWith('/api/updates') && r.request().method() === 'POST',
      ),
      page
        .getByRole('switch', { name: 'Check for updates automatically' })
        .uncheck(),
    ]);
    assert.strictEqual(state.automatic, false);
    assert.strictEqual(state.channel, 'nightly');
    assert(
      !actions.includes('download'),
      'A channel choice must not download an update',
    );
    await page
      .getByRole('button', { name: 'Check for updates', exact: true })
      .click();
    await page
      .getByRole('button', { name: 'Download update', exact: true })
      .click();
    await page.getByRole('button', { name: 'Close updates' }).click();
    await page.getByRole('button', { name: 'Close settings' }).click();
    await open();
    await page
      .getByRole('button', { name: 'Restart Dash and install', exact: true })
      .waitFor({ timeout: 8000 });
    assert.strictEqual(
      actions.filter((a) => a === 'download').length,
      1,
      'Closing Settings must not restart the download',
    );
    for (const size of [
      [1440, 900],
      [390, 844],
    ]) {
      await page.setViewportSize({ width: size[0], height: size[1] });
      await page.waitForTimeout(700);
      assert(
        await page
          .getByRole('dialog', { name: 'Dash updates', exact: true })
          .evaluate((element) => {
            const bounds = element.getBoundingClientRect();
            return (
              bounds.left >= 0 &&
              bounds.right <= innerWidth &&
              bounds.top >= 0 &&
              bounds.bottom <= innerHeight
            );
          }),
        'Update sheet must fit inside the viewport',
      );
      for (const theme of ['dark', 'light']) {
        await page.evaluate(
          (theme) => (document.documentElement.dataset.theme = theme),
          theme,
        );
        await page.waitForTimeout(200);
        const result = await new AxeBuilder({ page })
          .include('.scrim--over .panel')
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
    }
    driving = 'unknown';
    await page.waitForTimeout(2800);
    assert(
      await page
        .getByRole('button', { name: 'Restart Dash and install', exact: true })
        .isDisabled(),
    );
    driving = 'parked';
    await page.waitForTimeout(2800);
    const navigation = page.waitForEvent('framenavigated', {
      predicate: (frame) => frame === page.mainFrame(),
      timeout: 12000,
    });
    await page
      .getByRole('button', { name: 'Restart Dash and install', exact: true })
      .click();
    await page.getByRole('button', { name: 'Close updates' }).click();
    await page.getByRole('button', { name: 'Close settings' }).click();
    await navigation;
    assert(
      healthFinished,
      'The browser must wait for startup health before reloading',
    );
    await open();
    await page.getByText(state.current.version, { exact: false }).waitFor();
    await page.waitForTimeout(700);
    await page.screenshot({ path: '.artifacts/update-settings-mobile.png' });
    state.managed = false;
    state.current = {
      ...current,
      channel: 'local',
      version: '0.1.0-local.debug',
    };
    state.automatic = false;
    await page
      .getByText('Source build. Install a release to enable updates.', {
        exact: true,
      })
      .waitFor();
    assert.strictEqual(
      await page
        .getByRole('combobox', { name: 'Delivery channel', exact: true })
        .count(),
      0,
    );
    assert.strictEqual(
      await page
        .getByRole('button', { name: 'Download update', exact: true })
        .count(),
      0,
    );
    console.log(
      'Updates UI passed: nested Settings sheet, retry after failed load, three channels, explicit check/download/restart, automatic-check preference, retained downloads across dismissal, unknown-state lockout, health-aware reload after dismissing Settings on HTTP LAN, mobile fit and accessibility.',
    );
    await context.close();
  } finally {
    await browser.close();
  }
});
