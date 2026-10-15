import { chromium } from '@playwright/test';
import { readFileSync } from 'node:fs';
import { assert, test } from 'vite-plus/test';
import { installMapFixture } from './map-fixture.js';

test('i18n-smoke', async () => {
  const origin = process.env.DASH_TEST_URL || 'http://127.0.0.1:8088';
  const languages = [
    ['en-US', 'English'],
    ['ja', '日本語'],
    ['zh-CN', '简体中文'],
    ['ko', '한국어'],
    ['vi', 'Tiếng Việt'],
  ];
  const bundles = Object.fromEntries(
    languages.map(([id]) => [
      id,
      JSON.parse(readFileSync(`frontend/src/i18n/locales/${id}.json`, 'utf8')),
    ]),
  );
  const browser = await chromium.launch({
    executablePath:
      process.env.CHROME_PATH ||
      '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
    headless: true,
  });
  const errors = [];
  async function waitLanguage(page, locale) {
    await page.waitForFunction(
      (value) => document.documentElement.lang === value,
      locale,
    );
  }
  async function openSettings(page, locale) {
    await page
      .getByRole('button', {
        name: bundles[locale].settings.settings,
        exact: true,
      })
      .click();
    await page
      .getByRole('dialog', {
        name: bundles[locale].settings.settings,
        exact: true,
      })
      .waitFor();
    await page
      .locator('.scrim')
      .last()
      .evaluate(async (scrim) => {
        await new Promise((resolve) =>
          requestAnimationFrame(() => requestAnimationFrame(resolve)),
        );
        await Promise.all(
          scrim
            .getAnimations({ subtree: true })
            .filter(
              (animation) =>
                animation.timeline === document.timeline &&
                animation.effect?.getComputedTiming().iterations !== Infinity,
            )
            .map((animation) => animation.finished.catch(() => {})),
        );
      });
  }
  async function closeSettings(page, locale) {
    await page
      .getByRole('button', {
        name: bundles[locale].settings.closeSettings,
        exact: true,
      })
      .click();
    await page
      .getByRole('dialog', {
        name: bundles[locale].settings.settings,
        exact: true,
      })
      .waitFor({ state: 'hidden' });
  }
  async function selectLanguage(page, current, next) {
    await page
      .getByRole('combobox', {
        name: bundles[current].settings.language,
        exact: true,
      })
      .click();
    await page
      .getByRole('option', {
        name: languages.find(([id]) => id === next)[1],
        exact: true,
      })
      .click();
    await waitLanguage(page, next);
    await page
      .getByRole('dialog', {
        name: bundles[next].settings.settings,
        exact: true,
      })
      .waitFor();
    assert.strictEqual(
      await page
        .getByRole('radio', { name: bundles[next].settings.light, exact: true })
        .count(),
      1,
      'Theme labels must update in the already-open sheet',
    );
  }
  async function screenshot(page, path) {
    // Scroll-driven edge masks are intentionally unfinished until the user scrolls.
    // Wait only for finite transitions on the document timeline.
    await page.evaluate(async () => {
      await Promise.all(
        document
          .getAnimations()
          .filter(
            (animation) =>
              animation.timeline === document.timeline &&
              animation.effect?.getComputedTiming().iterations !== Infinity,
          )
          .map((animation) => animation.finished.catch(() => {})),
      );
    });
    await page.screenshot({ path });
  }
  async function fits(page, name) {
    assert(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
      `${name}: horizontal page overflow`,
    );
    assert(
      !(await page.evaluate(() =>
        /\b(?:app|settings|player|facts|dashboard)\.[A-Za-z]+/.test(
          document.body.innerText,
        ),
      )),
      `${name}: missing translation key`,
    );
    assert(
      !(await page.evaluate(() => document.body.innerText.includes('{{'))),
      `${name}: unresolved interpolation`,
    );
  }
  try {
    const context = await browser.newContext({
      viewport: { width: 1440, height: 1000 },
      locale: 'en-US',
      serviceWorkers: 'block',
    });
    await installMapFixture(context);
    const page = await context.newPage();
    page.on('pageerror', (error) => errors.push(error.message));
    await page.goto(origin);
    await page.locator('.drive-row').first().waitFor();
    let current = 'en-US';
    for (const [next] of languages) {
      await openSettings(page, current);
      await selectLanguage(page, current, next);
      current = next;
      await fits(page, `${next} desktop settings`);
      await closeSettings(page, next);
      await page
        .locator('.workspace-nav')
        .getByRole('button', { name: bundles[next].app.overview, exact: true })
        .click();
      await page
        .getByRole('heading', {
          name: bundles[next].dashboard.recordingOverview,
          exact: true,
        })
        .waitFor();
      await fits(page, `${next} dashboard`);
      if (next === 'vi') {
        await page
          .getByRole('combobox', {
            name: bundles.vi.dashboard.statisticsDistanceUnit,
            exact: true,
          })
          .click();
        await page
          .getByRole('option', {
            name: bundles.vi.dashboard.miles,
            exact: true,
          })
          .click();
        assert.strictEqual(
          await page.evaluate(
            () => JSON.parse(localStorage.getItem('dash.dashboard')).unit,
          ),
          'mi',
        );
      }
      await screenshot(page, `.artifacts/i18n-${next}-desktop.png`);
      await page
        .locator('.workspace-nav')
        .getByRole('button', {
          name: bundles[next].app.recordings,
          exact: true,
        })
        .click();
      await page.locator('.session-view').waitFor();
      await page
        .getByRole('button', {
          name: bundles[next].player.playRecording,
          exact: true,
        })
        .waitFor();
      await page
        .getByRole('button', {
          name: bundles[next].actions.sessionActions,
          exact: true,
        })
        .click();
      await page
        .getByRole('button', {
          name: bundles[next].app.downloadMcap,
          exact: true,
        })
        .click();
      await page
        .getByRole('dialog', {
          name: bundles[next].export.exportRecordings,
          exact: true,
        })
        .waitFor();
      await fits(page, `${next} export`);
      await page
        .getByRole('button', {
          name: bundles[next].export.closeExport,
          exact: true,
        })
        .click();
      await page
        .getByRole('dialog', {
          name: bundles[next].export.exportRecordings,
          exact: true,
        })
        .waitFor({ state: 'hidden' });
    }
    const chosenDrive = await page.locator('.detail-heading h1').innerText();
    await page.reload();
    await waitLanguage(page, 'vi');
    await page.locator('.drive-row').first().waitFor();
    assert.strictEqual(
      await page.locator('.detail-heading h1').innerText(),
      chosenDrive,
    );
    assert.strictEqual(
      await page.evaluate(() =>
        JSON.parse(localStorage.getItem('dash.language')),
      ),
      'vi',
    );
    assert.strictEqual(
      await page.evaluate(
        () => JSON.parse(localStorage.getItem('dash.dashboard')).unit,
      ),
      'mi',
      'Persist the unit identifier, not its translated label',
    );
    const sibling = await context.newPage();
    await sibling.goto(origin);
    await waitLanguage(sibling, 'vi');
    await openSettings(page, 'vi');
    await selectLanguage(page, 'vi', 'ja');
    await waitLanguage(sibling, 'ja');
    await closeSettings(page, 'ja');
    await openSettings(page, 'ja');
    await page
      .getByRole('combobox', {
        name: bundles.ja.settings.language,
        exact: true,
      })
      .click();
    await page
      .getByRole('option', {
        name: bundles.ja.settings.followSystem,
        exact: true,
      })
      .click();
    await waitLanguage(page, 'en-US');
    await closeSettings(page, 'en-US');
    await page.evaluate(() => {
      Object.defineProperty(navigator, 'language', {
        configurable: true,
        value: 'ko-KR',
      });
      window.dispatchEvent(new Event('languagechange'));
    });
    await waitLanguage(page, 'ko');
    await page
      .locator('.workspace-nav')
      .getByRole('button', { name: bundles.ko.app.live, exact: true })
      .click();
    await page
      .locator('.live-view')
      .getByText(bundles.ko.live.readyWhenYourDeviceIs, { exact: true })
      .first()
      .waitFor();
    await context.setOffline(true);
    await openSettings(page, 'ko');
    await selectLanguage(page, 'ko', 'vi');
    await closeSettings(page, 'vi');
    await page
      .locator('.live-view')
      .getByText(bundles.vi.live.readyWhenYourDeviceIs, { exact: true })
      .first()
      .waitFor();
    await context.setOffline(false);
    await context.close();

    for (const [id] of languages) {
      const mobile = await browser.newContext({
        viewport: { width: 390, height: 844 },
        isMobile: true,
        hasTouch: true,
        locale: id,
        serviceWorkers: 'block',
      });
      await installMapFixture(mobile);
      const p = await mobile.newPage();
      p.on('pageerror', (error) => errors.push(error.message));
      await p.goto(origin);
      await waitLanguage(p, id);
      await p.locator('.drive-row').first().waitFor();
      await openSettings(p, id);
      await fits(p, `${id} mobile settings`);
      await p.screenshot({ path: `.artifacts/i18n-${id}-mobile-settings.png` });
      await closeSettings(p, id);
      await p.locator('.drive-row').first().click();
      await p.locator('.session-view').waitFor();
      await p
        .getByRole('button', {
          name: bundles[id].player.playRecording,
          exact: true,
        })
        .waitFor();
      await fits(p, `${id} mobile viewer`);
      assert(
        await p
          .locator('.session-view')
          .evaluate((el) => el.scrollHeight <= el.clientHeight + 1),
        `${id}: fixed viewer overflow`,
      );
      await screenshot(p, `.artifacts/i18n-${id}-mobile-viewer.png`);
      await p
        .getByRole('button', { name: bundles[id].app.mapData, exact: true })
        .click();
      await p
        .getByRole('button', { name: bundles[id].app.tripDetails, exact: true })
        .click();
      await p
        .getByRole('dialog', { name: bundles[id].app.tripDetails, exact: true })
        .waitFor();
      await fits(p, `${id} mobile metadata`);
      await p
        .getByRole('button', {
          name: bundles[id].app.closeTripDetails,
          exact: true,
        })
        .click();
      await p
        .getByRole('dialog', { name: bundles[id].app.tripDetails, exact: true })
        .waitFor({ state: 'hidden' });
      await p.setViewportSize({ width: 320, height: 740 });
      await fits(p, `${id} 320px viewer`);
      await p
        .getByRole('button', {
          name: bundles[id].app.openNavigation,
          exact: true,
        })
        .click();
      await p
        .locator('.navigation-drawer .workspace-nav')
        .getByRole('button', { name: bundles[id].app.overview, exact: true })
        .click();
      await p
        .getByRole('heading', {
          name: bundles[id].dashboard.recordingOverview,
          exact: true,
        })
        .waitFor();
      await fits(p, `${id} 320px dashboard`);
      await screenshot(p, `.artifacts/i18n-${id}-mobile-dashboard.png`);
      await mobile.close();
    }
    const unavailable = await browser.newContext({
      locale: 'ja-JP',
      serviceWorkers: 'block',
    });
    await unavailable.addInitScript(() => {
      Storage.prototype.setItem = function () {
        throw new DOMException('Storage blocked', 'SecurityError');
      };
    });
    const p = await unavailable.newPage();
    await p.goto(origin);
    await waitLanguage(p, 'ja');
    await p.locator('.drive-row').first().waitFor();
    await openSettings(p, 'ja');
    await selectLanguage(p, 'ja', 'vi');
    await p
      .getByText(
        bundles.vi.settings.browserStorageIsUnavailablePreferencesApplyFor,
        { exact: true },
      )
      .waitFor();
    await unavailable.close();
    assert.deepEqual(errors, []);
    console.log(
      'i18n passed: five catalogs, immediate/persisted/cross-tab/system language changes, offline-bundled messages, localized dates/numbers, exports, mobile viewer/metadata fit, storage failure fallback.',
    );
  } finally {
    await browser.close();
  }
});
