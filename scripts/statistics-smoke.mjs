import { chromium } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import assert from 'node:assert/strict';
const origin = process.env.DASH_TEST_URL || 'http://127.0.0.1:8089';
const info = await (await fetch(`${origin}/api/info`)).json();
assert(
  info.simulation,
  'Statistics integration uses only the synthetic sandbox',
);
async function state(value) {
  await fetch(`${origin}/api/simulation/state`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ state: value }),
  });
}
await state('parked');
const browser = await chromium.launch({
  executablePath:
    process.env.CHROME_PATH ||
    '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
  headless: true,
});
try {
  const context = await browser.newContext({
    viewport: { width: 1440, height: 1000 },
    colorScheme: 'light',
  });
  const page = await context.newPage();
  await page.clock.setFixedTime(new Date('2026-10-13T12:00:00'));
  const errors = [];
  page.on('pageerror', (e) => errors.push(e.message));
  let decodedRequests = 0;
  page.on('request', (req) => {
    if (req.url().includes('/api/metadata')) decodedRequests++;
  });
  await page.goto(origin);
  await page.waitForFunction(
    () => document.querySelector('video')?.duration > 15,
  );
  await page.getByRole('button', { name: 'Overview', exact: true }).click();
  await page.getByRole('heading', { name: 'Recording overview' }).waitFor();
  await page.waitForFunction(() =>
    document
      .querySelector('.dashboard-coverage')
      ?.textContent.includes('6 of 6 segments'),
  );
  const initialRequests = decodedRequests;
  await page
    .getByRole('button', { name: 'Graph recorded time', exact: true })
    .click();
  assert.match(
    await page.locator('.chart-heading').innerText(),
    /Recorded time/,
  );
  const bars = page.locator('.activity-hit');
  assert.equal(await bars.count(), 2);
  await bars.first().hover();
  assert.equal(
    await page.locator('.dashboard-drive').count(),
    3,
    'Hover must inspect without filtering',
  );
  await bars.first().click();
  assert.equal(await page.locator('.dashboard-drive').count(), 1);
  await page.keyboard.press('ArrowRight');
  assert.equal(
    await page.locator('.dashboard-drive').count(),
    1,
    'Arrow navigation must not change the pinned selection',
  );
  await page.keyboard.press('Enter');
  assert.equal(await page.locator('.dashboard-drive').count(), 2);
  await page.keyboard.press('Escape');
  assert.equal(await page.locator('.dashboard-drive').count(), 3);
  await page.getByRole('button', { name: 'Graph drives', exact: true }).click();
  await page
    .getByRole('group', { name: 'Group activity', exact: true })
    .getByRole('button', { name: 'Drives', exact: true })
    .click();
  assert.equal(await bars.count(), 3);
  await page
    .getByRole('combobox', { name: 'Statistics distance unit' })
    .click();
  await page.getByRole('option', { name: 'Miles', exact: true }).click();
  await page
    .getByRole('button', { name: 'Graph gps distance', exact: true })
    .click();
  assert.match(await page.locator('.chart-heading').innerText(), /\(mi\)/);
  await page.getByText('View chart as table', { exact: true }).click();
  await page
    .locator('.chart-table tbody')
    .getByRole('button', { name: 'Show drives', exact: true })
    .first()
    .click();
  assert.equal(await page.locator('.dashboard-drive').count(), 1);
  await page.getByText('View chart as table', { exact: true }).click();
  await page.locator('.dashboard-drive').first().click();
  await page.locator('video').waitFor();
  await page.getByRole('button', { name: 'Overview', exact: true }).click();
  await page.locator('.dashboard-drive').waitFor();
  assert.equal(
    await page.locator('.dashboard-drive').count(),
    1,
    'Returning from a recording must preserve the graph selection',
  );
  assert.equal(
    await page
      .getByRole('combobox', { name: 'Statistics distance unit' })
      .innerText(),
    'Miles',
  );
  await page
    .getByRole('button', { name: 'Clear selection', exact: true })
    .click();
  await page.getByRole('combobox', { name: 'Statistics period' }).click();
  await page.getByRole('option', { name: 'Last 7 days', exact: true }).click();
  assert.equal(await page.locator('.dashboard-drive').count(), 3);
  await page
    .getByRole('group', { name: 'Group activity', exact: true })
    .getByRole('button', { name: 'Months', exact: true })
    .click();
  assert.equal(await bars.count(), 1);
  await page.waitForTimeout(2800);
  assert.equal(
    decodedRequests,
    initialRequests + 1,
    'Only opening a recording may request detailed metadata; charts use summaries',
  );
  for (const [width, height, theme] of [
    [1440, 1000, 'light'],
    [936, 768, 'light'],
    [390, 844, 'light'],
    [390, 844, 'dark'],
    [844, 390, 'dark'],
  ]) {
    await page.setViewportSize({ width, height });
    await page.emulateMedia({ colorScheme: theme });
    await page.waitForTimeout(200);
    assert(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
      'Dashboard must not overflow horizontally',
    );
    const axe = await new AxeBuilder({ page })
      .withTags(['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'])
      .analyze();
    assert.deepEqual(
      axe.violations.map((v) => ({
        id: v.id,
        nodes: v.nodes.map((n) => ({
          target: n.target,
          reason: n.failureSummary,
        })),
      })),
      [],
    );
    await page.screenshot({
      path: `.artifacts/statistics-${width}-${theme}.png`,
    });
  }
  // A separate browser-only history exercises dense and partial libraries.
  // It does not create folders or telemetry on the simulated device.
  const denseContext = await browser.newContext({
    viewport: { width: 390, height: 844 },
  });
  const dense = await denseContext.newPage();
  await dense.clock.setFixedTime(new Date('2026-10-13T12:00:00'));
  dense.on('pageerror', (e) => errors.push(e.message));
  const dirs = [],
    segments = {};
  for (let n = 0; n < 90; n++) {
    const date = new Date('2026-10-13T12:00:00Z');
    date.setUTCDate(date.getUTCDate() - n);
    const id = `${date.toISOString().slice(0, 10)}--12-00-00`;
    for (let part = 0; part < 2; part++) {
      const name = `${id}--${part}`;
      dirs.push({ href: `${name}/`, ts: +date / 1000, sz: 0 });
      segments[name] = {
        files: [],
        metadata: part
          ? { status: 'error' }
          : {
              status: 'ready',
              duration: 120,
              distanceKm: n % 4 === 3 ? 1.25 : 0,
              distanceSamples: n % 4 === 0 || n % 4 === 3 ? 1 : 0,
            },
      };
    }
  }
  await dense.route(/\/files\/\?/, (route) =>
    route.fulfill({ json: { dirs, files: [] } }),
  );
  await dense.route('**/api/index', (route) =>
    route.fulfill({
      json: {
        version: 1,
        status: {
          completed: 180,
          total: 180,
          errors: 0,
          running: false,
          driving: 'unknown',
        },
        segments,
      },
    }),
  );
  await dense.goto(origin);
  await dense.getByRole('button', { name: 'Open navigation' }).click();
  await dense.getByRole('button', { name: 'Overview', exact: true }).click();
  await dense.waitForFunction(() =>
    document
      .querySelector('.dashboard-coverage')
      ?.textContent.includes('90 of 180'),
  );
  assert(
    (await dense.locator('.activity-hit').count()) <= 10,
    'Dense phone charts must bound the rendered bars',
  );
  assert(
    await dense.getByRole('button', { name: 'Earlier activity' }).isEnabled(),
  );
  await dense.getByRole('button', { name: 'Earlier activity' }).click();
  await dense.locator('.activity-hit').first().click();
  assert.equal(await dense.locator('.dashboard-drive').count(), 1);
  await dense
    .getByRole('button', { name: 'Clear selection', exact: true })
    .click();
  assert.equal(
    await dense.locator('.dashboard-drive').count(),
    20,
    'Drive lists must be paged',
  );
  await dense.getByRole('button', { name: 'Next drives', exact: true }).click();
  assert.match(
    await dense.locator('.dashboard-list-paging').innerText(),
    /21–40 of 90/,
  );
  await dense.getByRole('combobox', { name: 'Statistics period' }).click();
  await dense.getByRole('option', { name: 'Last 7 days', exact: true }).click();
  assert.equal(await dense.locator('.dashboard-drive').count(), 7);
  // With every GPS value unavailable, the total must be a dash, never zero.
  for (const entry of Object.values(segments)) {
    entry.metadata.distanceKm = 0;
    entry.metadata.distanceSamples = 0;
  }
  await dense.reload();
  await dense.getByRole('button', { name: 'Open navigation' }).click();
  await dense.getByRole('button', { name: 'Overview', exact: true }).click();
  await dense.waitForFunction(() =>
    document
      .querySelector('.dashboard-coverage')
      ?.textContent.includes('7 of 14'),
  );
  assert.match(
    await dense
      .getByRole('button', { name: 'Graph gps distance', exact: true })
      .innerText(),
    /—/,
  );
  assert.equal(await dense.locator('.activity-bar').count(), 0);
  const missingAudit = await new AxeBuilder({ page: dense })
    .withTags(['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'])
    .analyze();
  assert.deepEqual(
    missingAudit.violations.map((v) => v.id),
    [],
  );
  await denseContext.close();
  assert.deepEqual(errors, []);
  console.log(
    'Statistics passed: summary totals, graph metric/group/unit/period controls, hover vs selection, keyboard filtering, table alternative, recording navigation and restored selection, no dashboard metadata fetches, responsive light/dark accessibility.',
  );
} finally {
  await browser.close();
  await state('unknown');
}
