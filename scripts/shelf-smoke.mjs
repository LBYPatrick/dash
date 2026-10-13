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
  });
  const page = await context.newPage();
  const errors = [];
  page.on('pageerror', (e) => errors.push(e.message));
  // UI-only parked fixture. Never change actual device driving params.
  await page.route('**/api/index', async (route) => {
    const response = await route.fetch();
    const snapshot = await response.json();
    snapshot.status.driving = 'parked';
    await route.fulfill({ response, json: snapshot });
  });
  await page.goto(origin);
  await page.getByRole('heading', { name: 'Your drives' }).waitFor();
  async function settled() {
    await page.waitForFunction(() =>
      [
        ...document.querySelectorAll(
          'dialog[open], .scrim, [role="dialog"], :popover-open',
        ),
      ].every((e) => +getComputedStyle(e).opacity >= 0.999),
    );
    // Sheet height can still be moving after its entrance opacity has settled.
    await page.waitForTimeout(310);
  }
  async function audit(name) {
    await settled();
    const result = await new AxeBuilder({ page })
      .withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa', 'wcag22aa'])
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
    console.log(`Shelf audit: ${name}`);
  }
  await audit('recordings');
  for (const view of ['Overview', 'Live', 'Recordings']) {
    await page.getByRole('button', { name: view, exact: true }).click();
    await audit(view);
  }
  await page.getByLabel('Session actions', { exact: true }).click();
  await audit('session actions');
  await page.getByRole('button', { name: 'Download ZIP', exact: true }).click();
  const sheet = page.getByRole('dialog', { name: 'Export recordings' });
  await audit('ZIP sheet');
  const before = await sheet.boundingBox();
  const format = page.getByRole('combobox', { name: 'Format', exact: true });
  await format.click();
  await audit('format menu inside modal');
  await page.keyboard.press('Escape');
  assert.equal(await page.locator('.menulist:popover-open').count(), 0);
  assert(await sheet.isVisible(), 'Escape on selector must not dismiss sheet');
  await format.click();
  await page
    .getByRole('option', {
      name: 'MP4 · one continuous camera recording',
      exact: true,
    })
    .click();
  await settled();
  const after = await sheet.boundingBox();
  assert(
    after.height > before.height + 20,
    'sheet must grow with camera options',
  );
  assert(
    Math.abs(after.y + after.height / 2 - 450) < 3,
    'sheet grows about its centre',
  );
  assert.equal(
    await page
      .getByRole('combobox', { name: /From segment|Through segment/ })
      .count(),
    0,
  );
  await audit('MP4 sheet');
  await page.route('**/api/exports', (route) =>
    route.fulfill({
      status: 503,
      json: { error: 'Device busy. Try again while parked.' },
    }),
  );
  await page
    .getByRole('button', { name: 'Export recording', exact: true })
    .click();
  await page.getByRole('alert').filter({ hasText: 'Device busy' }).waitFor();
  await audit('export failure and retry');
  await page.unroute('**/api/exports');
  await page.keyboard.press('Escape');
  await sheet.waitFor({ state: 'hidden' });
  assert(
    await page
      .getByLabel('Session actions', { exact: true })
      .evaluate((e) => e === document.activeElement),
    'sheet must restore opener focus',
  );
  const search = page.getByPlaceholder('Find a date or drive…');
  await search.fill('no recording can match this');
  await page.getByRole('heading', { name: 'No matching drives' }).waitFor();
  await audit('empty search recovery');
  await search.fill('');
  // Sort menus reopen reliably after both mouse and keyboard selections.
  const sort = page.getByRole('combobox', { name: /Sort/ });
  await sort.click();
  await audit('sort menu');
  await page.keyboard.press('ArrowDown');
  await page.keyboard.press('Enter');
  await sort.click();
  assert.equal(await page.locator('.menulist:popover-open').count(), 1);
  await page.keyboard.press('Escape');
  for (const [trigger, title, close] of [
    ['Settings', 'Settings', 'Close settings'],
    ['Switch device', 'Device', 'Close device settings'],
    ['Help and installation', 'Help', 'Close help'],
  ]) {
    await page.getByRole('button', { name: trigger, exact: true }).click();
    await audit(`${title} sheet`);
    // Non-scrolling sheets must not reserve scrollbar space on the panel
    // or body. The measured content should have equal horizontal padding.
    const geometry = await page.locator('.shelf-sheet').evaluate((panel) => {
      const body = panel.querySelector('.panel__body');
      const content = panel.querySelector('.panel__measure');
      const box = panel.getBoundingClientRect(),
        inner = content.getBoundingClientRect();
      return {
        panelGap: panel.offsetWidth - panel.clientWidth,
        bodyGap: body.offsetWidth - body.clientWidth,
        left: inner.left - box.left,
        right: box.right - inner.right,
        overflowing: body.scrollHeight > body.clientHeight + 1,
      };
    });
    assert.equal(geometry.panelGap, 0, `${title}: phantom panel gutter`);
    if (!geometry.overflowing) {
      assert.equal(geometry.bodyGap, 0, `${title}: phantom body gutter`);
      assert(
        Math.abs(geometry.left - geometry.right) < 1,
        `${title}: asymmetric content padding`,
      );
    }

    await page.getByRole('button', { name: close, exact: true }).click();
  }
  // Compact landscape keeps actions in the top layer, close buttons visible,
  // and overflow confined to sheet content rather than the session viewport.
  await page.setViewportSize({ width: 844, height: 390 });
  await page.locator('.drive-row').first().click();
  await page.getByLabel('Session actions', { exact: true }).click();
  await audit('landscape actions');
  const menu = await page
    .locator('.session-action-menu:popover-open')
    .boundingBox();
  assert(menu.y >= 0 && menu.y + menu.height <= 391);
  await page.getByRole('button', { name: 'Download MP4', exact: true }).click();
  await audit('landscape sheet');
  assert(
    await sheet
      .locator('.shelf-sheet-body')
      .evaluate(
        (e) =>
          e.scrollHeight > e.clientHeight &&
          getComputedStyle(e).overflowY === 'auto',
      ),
  );
  const close = await page
    .getByRole('button', { name: 'Close export' })
    .boundingBox();
  assert(close.y >= 0 && close.y + close.height <= 390);
  assert(
    await page.evaluate(
      () =>
        document.documentElement.scrollHeight <= innerHeight &&
        document.documentElement.scrollWidth <= innerWidth,
    ),
  );
  await page.getByRole('button', { name: 'Close export' }).click();
  await page.setViewportSize({ width: 390, height: 844 });
  await audit('phone viewer');
  await page.screenshot({ path: '.artifacts/shelf-phone.png' });
  assert.deepEqual(errors, []);
  console.log(
    'Shelf component, keyboard, sheet sizing and accessibility audit passed.',
  );
} finally {
  await browser.close();
}
