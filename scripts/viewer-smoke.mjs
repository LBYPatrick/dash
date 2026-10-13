import { chromium } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import assert from 'node:assert/strict';
const origin = process.env.DASH_TEST_URL || 'http://127.0.0.1:8089';
const info = await (await fetch(`${origin}/api/info`)).json();
assert(info.simulation, 'Viewer integration changes only a synthetic sandbox');
await fetch(`${origin}/api/simulation/state`, {
  method: 'POST',
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ state: 'parked' }),
});
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
  // Exercise recorded engagement presentation without pretending synthetic logs
  // contain real firmware engagement messages. Decoder wire tests cover that.
  await page.route('**/api/metadata?*', async (route) => {
    const response = await route.fetch();
    const entries = await response.json();
    for (const entry of Object.values(entries)) {
      const m = entry.metadata;
      const anchor = m.startMonotonic || m.points?.[0]?.monotonic || 100;
      m.startMonotonic = anchor;
      m.engagement = [
        { time: anchor, enabled: false, active: false },
        { time: anchor + 2, enabled: true, active: true },
        { time: anchor + 6, enabled: true, active: false },
      ];
    }
    await route.fulfill({ response, json: entries });
  });
  await page.goto(origin);
  await page.waitForFunction(
    () => document.querySelector('video')?.duration >= 15,
  );
  const grip = page.getByRole('separator', {
    name: 'Resize recording sidebar',
  });
  const initial = await page.locator('.sidebar').boundingBox();
  const box = await grip.boundingBox();
  await page.mouse.move(box.x + 6, box.y + box.height / 2);
  await page.mouse.down();
  await page.mouse.move(box.x + 86, box.y + box.height / 2, { steps: 8 });
  await page.mouse.up();
  assert(
    (await page.locator('.sidebar').boundingBox()).width > initial.width + 70,
  );
  await page.reload();
  await page.waitForFunction(
    () => document.querySelector('video')?.duration >= 15,
  );
  assert((await page.locator('.sidebar').boundingBox()).width > 390);
  await grip.dblclick();
  assert.equal(
    await page.locator('.workspace').getAttribute('data-sidebar-resetting'),
    'true',
  );
  await page.waitForFunction(
    () =>
      Math.abs(
        document.querySelector('.sidebar').getBoundingClientRect().width - 320,
      ) < 0.5,
  );
  assert(
    await page
      .locator('.session-data')
      .evaluate((e) => getComputedStyle(e).padding === '0px'),
  );
  await page.getByRole('button', { name: 'Enter playback timestamp' }).click();
  await page.getByRole('textbox', { name: 'Playback timestamp' }).fill('0:03');
  await page.getByRole('button', { name: 'Go', exact: true }).click();
  await page.waitForFunction(
    () => Math.abs(document.querySelector('video').currentTime - 3) < 0.2,
  );
  await page.getByRole('combobox', { name: 'Playback speed' }).click();
  await page.getByRole('option', { name: '2×', exact: true }).click();
  assert.equal(await page.locator('video').evaluate((e) => e.playbackRate), 2);
  await page.getByRole('button', { name: 'Enter playback timestamp' }).click();
  await page.getByRole('textbox', { name: 'Playback timestamp' }).fill('1:99');
  await page.getByRole('button', { name: 'Go', exact: true }).click();
  await page.getByRole('alert').filter({ hasText: 'Enter a time' }).waitFor();
  await page.keyboard.press('Escape');
  await page
    .getByRole('textbox', { name: 'Playback timestamp' })
    .waitFor({ state: 'hidden' });
  assert.equal(
    await page
      .getByRole('button', { name: 'Enter playback timestamp' })
      .evaluate((e) => e === document.activeElement),
    true,
  );
  const slider = page.getByRole('slider', { name: 'Recording timeline' });
  await slider.fill('12');
  await page.waitForFunction(
    () => Math.abs(document.querySelector('video').currentTime - 12) < 0.2,
  );
  await slider.fill('3');
  await page.getByRole('button', { name: 'Follow', exact: true }).waitFor();
  await page.getByRole('button', { name: 'Full route', exact: true }).click();
  assert.equal(
    await page
      .getByRole('button', { name: 'Follow', exact: true })
      .getAttribute('aria-pressed'),
    'false',
  );
  await page.getByRole('button', { name: 'Follow', exact: true }).click();
  assert.equal(
    await page
      .getByRole('button', { name: 'Follow', exact: true })
      .getAttribute('aria-pressed'),
    'true',
  );
  await page
    .getByRole('button', { name: 'Viewer options', exact: true })
    .click();
  await page
    .getByRole('button', { name: 'Show snapshots', exact: true })
    .click();
  await page
    .getByRole('button', { name: 'Show engagement status', exact: true })
    .click();
  await page.keyboard.press('Escape');
  await page.locator('.snapshot-strip button').first().waitFor();
  assert.match(await page.locator('.engagement-row').innerText(), /Engaged/);
  await page.locator('.snapshot-strip button').last().click();
  await page.waitForFunction(
    () => document.querySelector('video').currentTime >= 7.9,
  );
  for (const size of [
    [1440, 900],
    [390, 844],
    [844, 390],
  ]) {
    await page.setViewportSize({ width: size[0], height: size[1] });
    if (size[0] <= 900 && !(await slider.isVisible()))
      await page.locator('.drive-row').first().click();
    await page.waitForTimeout(200);
    assert(
      await page.evaluate(
        () =>
          document.documentElement.scrollHeight <= innerHeight &&
          document.documentElement.scrollWidth <= innerWidth,
      ),
    );
    const result = await new AxeBuilder({ page })
      .withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa', 'wcag22aa'])
      .analyze();
    assert.deepEqual(
      result.violations.map((v) => ({
        id: v.id,
        nodes: v.nodes.map((n) => n.target),
      })),
      [],
    );
    await page.screenshot({
      path: `.artifacts/enhanced-viewer-${size[0]}.png`,
    });
  }
  assert.deepEqual(errors, []);
  console.log(
    'Enhanced viewer passed: sidebar drag/persistence/animated reset, padding, typed/slider seeking, speed, follow/full-route, snapshots/engagement, responsive fit and accessibility.',
  );
} finally {
  await browser.close();
  await fetch(`${origin}/api/simulation/state`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ state: 'unknown' }),
  });
}
