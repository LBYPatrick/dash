import { chromium } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import { assert, test } from 'vite-plus/test';
import { installMapFixture } from './map-fixture.js';

async function settleSheet(page) {
  await page
    .locator('.scrim')
    .last()
    .evaluate(async (element) => {
      await new Promise((resolve) =>
        requestAnimationFrame(() => requestAnimationFrame(resolve)),
      );
      await Promise.all(
        element
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

test('batch export: mobile and desktop USB, progress, recovery and cloud authorization', async () => {
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
    for (const { viewport, colorScheme } of [
      { viewport: { width: 390, height: 844 }, colorScheme: 'light' },
      { viewport: { width: 390, height: 844 }, colorScheme: 'dark' },
      { viewport: { width: 1440, height: 900 }, colorScheme: 'light' },
      { viewport: { width: 1440, height: 900 }, colorScheme: 'dark' },
    ]) {
      const context = await browser.newContext({ viewport, colorScheme });
      await installMapFixture(context);
      const snapshot = {
        destinations: [],
        batches: [],
        authorizations: [],
        driving: 'parked',
        configurationSecure: false,
        usbSupported: true,
        bundledProviders: { gdrive: true, onedrive: true },
      };
      let batches = 0;
      await context.route('**/api/transfers**', async (route) => {
        const path = new URL(route.request().url()).pathname;
        if (path.endsWith('/volumes'))
          return route.fulfill({
            json: [
              {
                uuid: 'DASH-TEST-0001',
                label: 'Travel archive',
                filesystem: 'exfat',
                name: 'sda1',
                available: true,
                mounted: false,
              },
            ],
          });
        if (path.endsWith('/issues'))
          return route.fulfill({
            json: [
              { path: '00000001--abcdef0123--0/rlog.zst', status: 'missing' },
            ],
          });
        if (route.request().method() === 'POST') {
          if (path === '/api/transfers') {
            batches++;
            snapshot.batches = [
              {
                id: 'batch',
                destinationId: snapshot.destinations[0].id,
                status: 'running',
                totalFiles: 240,
                totalBytes: 1024000000,
                completedFiles: 12,
                completedBytes: 50000000,
                transferredBytes: 57000000,
                issues: 0,
                autoResume: true,
              },
            ];
          } else if (path.endsWith('/pause'))
            snapshot.batches[0].status = 'paused';
          else if (path.endsWith('/resume'))
            snapshot.batches[0].status = 'running';
          else if (path.endsWith('/cancel'))
            snapshot.batches[0].status = 'canceled';
        }
        await route.fulfill({ json: snapshot });
      });
      await context.route('**/api/transfer-destinations**', async (route) => {
        const request = route.request(),
          path = new URL(request.url()).pathname;
        if (path.endsWith('/authorize')) {
          const id = path.split('/').at(-2);
          const value = {
            destinationId: id,
            status: 'waiting',
            userCode: 'TEST-CODE',
            verificationUri: 'https://www.google.com/device',
            expiresAt: Math.floor(Date.now() / 1000) + 600,
          };
          snapshot.authorizations = [value];
          return route.fulfill({ json: value });
        }
        const input = request.postDataJSON();
        assert.deepEqual(input.secret, {});
        const value = {
          id: input.id,
          name: input.name,
          kind: input.kind,
          location: input.volumeUuid || '',
          connected: input.kind === 'usb',
        };
        snapshot.destinations.push(value);
        await route.fulfill({ json: value });
      });
      const page = await context.newPage();
      await page.goto(origin);
      await page
        .getByRole('button', { name: 'Settings', exact: true })
        .first()
        .click();
      await page
        .getByRole('button', { name: 'Export destinations', exact: true })
        .click();
      await page
        .getByRole('button', { name: 'Add destination', exact: true })
        .click();
      await page
        .getByRole('textbox', { name: 'Destination name', exact: true })
        .fill('Travel archive');
      await page
        .getByRole('button', { name: 'Save destination', exact: true })
        .click();
      await page.getByText('Travel archive', { exact: true }).last().waitFor();
      await settleSheet(page);
      let audit = await new AxeBuilder({ page })
        .withTags(['wcag2a', 'wcag2aa', 'wcag21aa'])
        .analyze();
      assert.deepEqual(
        audit.violations.map((v) => ({
          id: v.id,
          nodes: v.nodes.map((n) => ({
            target: n.target,
            failureSummary: n.failureSummary,
          })),
        })),
        [],
      );
      assert(
        await page.evaluate(
          () => document.documentElement.scrollWidth <= innerWidth,
        ),
      );
      // Return through the stacked Shelf sheets, then start a durable batch.
      await page.keyboard.press('Escape');
      await page
        .getByRole('button', { name: 'Copy all originals', exact: true })
        .click();
      await page.getByRole('button', { name: 'Pause', exact: true }).click();
      await page.getByRole('button', { name: 'Resume', exact: true }).waitFor();
      await page.getByRole('button', { name: 'Resume', exact: true }).click();
      assert.equal(batches, 1);
      await page
        .getByRole('button', { name: 'Cancel transfer', exact: true })
        .click();
      await page
        .getByRole('button', { name: 'Manage destinations', exact: true })
        .click();
      await page
        .getByRole('button', { name: 'Add destination', exact: true })
        .click();
      await page
        .getByRole('combobox', { name: 'Storage provider', exact: true })
        .click();
      await page
        .getByRole('option', { name: 'Google Drive', exact: true })
        .click();
      await page
        .getByRole('textbox', { name: 'Destination name', exact: true })
        .fill('My Drive');
      assert.equal(await page.locator('#transfer-client-id').count(), 0);
      assert.equal(await page.locator('#transfer-client-secret').count(), 0);
      await page
        .getByRole('button', { name: 'Save destination', exact: true })
        .click();
      await page.getByText('TEST-CODE', { exact: true }).waitFor();
      const link = page.getByRole('link', { name: 'Open authorization page' });
      assert.equal(
        await link.getAttribute('href'),
        'https://www.google.com/device',
      );
      await settleSheet(page);
      audit = await new AxeBuilder({ page })
        .withTags(['wcag2a', 'wcag2aa', 'wcag21aa'])
        .analyze();
      assert.deepEqual(
        audit.violations.map((v) => ({
          id: v.id,
          nodes: v.nodes.map((n) => ({
            target: n.target,
            failureSummary: n.failureSummary,
          })),
        })),
        [],
      );
      await context.close();
    }
  } finally {
    await browser.close();
  }
});

test('transfer sheets fit short phones in all five languages', async () => {
  const { readFileSync } = await import('node:fs');
  const browser = await chromium.launch({
    executablePath:
      process.env.CHROME_PATH ||
      '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
    headless: true,
    args: ['--disable-gpu', '--use-angle=swiftshader'],
  });
  try {
    for (const locale of ['en-US', 'ja', 'zh-CN', 'ko', 'vi']) {
      const labels = JSON.parse(
        readFileSync(`frontend/src/i18n/locales/${locale}.json`, 'utf8'),
      );
      const context = await browser.newContext({
        viewport: { width: 320, height: 568 },
        locale,
        colorScheme: 'dark',
        reducedMotion: 'reduce',
      });
      await installMapFixture(context);
      await context.route('**/api/transfers', (route) =>
        route.fulfill({
          json: {
            destinations: [],
            batches: [],
            authorizations: [],
            driving: 'parked',
            configurationSecure: false,
            usbSupported: true,
            bundledProviders: {},
          },
        }),
      );
      await context.route('**/api/transfers/volumes', (route) =>
        route.fulfill({ json: [] }),
      );
      const page = await context.newPage();
      await page.goto(process.env.DASH_TEST_URL || 'http://127.0.0.1:8088');
      await page
        .getByRole('button', { name: labels.settings.settings, exact: true })
        .first()
        .click();
      await page
        .getByRole('button', {
          name: labels.transfers.destinations,
          exact: true,
        })
        .click();
      await page
        .getByRole('button', {
          name: labels.transfers.addDestination,
          exact: true,
        })
        .click();
      await page
        .getByText(labels.transfers.noVolumes, { exact: true })
        .waitFor();
      const combo = page.getByRole('combobox', {
        name: labels.transfers.provider,
        exact: true,
      });
      await combo.click();
      await page
        .getByRole('option', { name: labels.transfers.s3Storage, exact: true })
        .click();
      await page
        .getByText(labels.transfers.secureSetup, { exact: true })
        .waitFor();
      const save = page.getByRole('button', {
        name: labels.transfers.saveDestination,
        exact: true,
      });
      await save.scrollIntoViewIfNeeded();
      assert(await save.isDisabled());
      assert(
        await page.evaluate(
          () => document.documentElement.scrollWidth <= innerWidth,
        ),
      );
      const bounds = await save.boundingBox();
      assert(bounds && bounds.y >= 0 && bounds.y + bounds.height <= 568);
      const audit = await new AxeBuilder({ page })
        .withTags(['wcag2a', 'wcag2aa', 'wcag21aa'])
        .analyze();
      assert.deepEqual(
        audit.violations.map((v) => ({
          id: v.id,
          nodes: v.nodes.map((n) => ({
            target: n.target,
            failureSummary: n.failureSummary,
          })),
        })),
        [],
      );
      await context.close();
    }
  } finally {
    await browser.close();
  }
});
