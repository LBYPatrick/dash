import { chromium } from '@playwright/test';
import { assert, test } from 'vite-plus/test';
import { installMapFixture } from './map-fixture.js';

test('MCAP waits for device completion and retries without conversion', async () => {
  const browser = await chromium.launch({
    executablePath: process.env.CHROME_PATH,
    headless: true,
  });
  try {
    const context = await browser.newContext();
    await installMapFixture(context);
    let submissions = 0;
    let completed = false;
    let polls = 0;
    await context.route('**/api/index', async (route) => {
      const response = await route.fetch();
      const data = await response.json();
      data.status.driving = 'parked';
      await route.fulfill({ response, json: data });
    });
    await context.route('**/api/exports', async (route) => {
      const request = route.request().postDataJSON();
      assert.equal(request.format, 'mcap');
      assert.equal(request.stream, false);
      submissions++;
      await route.fulfill({
        status: 202,
        json: { id: 'file-mcap', status: 'queued', name: 'session.mcap' },
      });
    });
    await context.route('**/api/exports/file-mcap', async (route) => {
      polls++;
      await route.fulfill({
        json: {
          id: 'file-mcap',
          name: 'session.mcap',
          status: completed ? 'complete' : 'running',
          progress: completed ? 1 : 0.5,
        },
      });
    });
    await context.route('**/api/exports/file-mcap/download', (route) =>
      route.fulfill({
        body: 'Synthetic completed-file handoff; container validity is tested in Go.',
        contentType: 'application/octet-stream',
        headers: {
          'content-disposition': 'attachment; filename="session.mcap"',
        },
      }),
    );
    const page = await context.newPage();
    const downloads = [];
    const errors = [];
    page.on('download', (download) => downloads.push(download));
    page.on('pageerror', (error) => errors.push(error.message));
    await page.goto(process.env.DASH_TEST_URL || 'http://127.0.0.1:8088');
    await page.locator('.session-layout').waitFor();
    await page
      .getByRole('button', { name: 'Session actions', exact: true })
      .click();
    await page
      .getByRole('button', { name: 'Download MCAP', exact: true })
      .click();
    await page.getByRole('dialog', { name: 'Export recordings' }).waitFor();
    await page
      .getByRole('button', { name: 'Download recording', exact: true })
      .click();
    await page.getByText('Preparing download… 50%', { exact: true }).waitFor();
    assert(polls > 0);
    assert.equal(
      downloads.length,
      0,
      'Download started before conversion finished',
    );
    const readyDownload = page.waitForEvent('download');
    completed = true;
    assert.equal((await readyDownload).suggestedFilename(), 'session.mcap');
    const retry = page.getByRole('link', {
      name: 'Download again',
      exact: true,
    });
    await retry.waitFor();
    const repeated = page.waitForEvent('download');
    await retry.click();
    await repeated;
    assert.equal(submissions, 1, 'Retry started a second conversion');
    assert.equal(downloads.length, 2);
    assert.deepEqual(errors, []);
  } finally {
    await browser.close();
  }
});
