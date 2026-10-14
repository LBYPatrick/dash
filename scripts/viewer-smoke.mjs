import { installMapFixture } from './map-fixture.mjs';
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
  await installMapFixture(context);
  const page = await context.newPage();
  const errors = [];
  page.on('pageerror', (e) => errors.push(e.message));
  // Exercise recorded engagement presentation without pretending synthetic logs
  // contain real firmware engagement messages. Decoder wire tests cover that.
  let recordedEngagement = true;
  await page.route('**/api/metadata?*', async (route) => {
    const response = await route.fetch();
    const entries = await response.json();
    for (const entry of Object.values(entries)) {
      const m = entry.metadata;
      const anchor = m.startMonotonic || m.points?.[0]?.monotonic || 100;
      m.startMonotonic = anchor;
      // A known browser-only GPS track makes motion measurable with MapLibre.
      m.points = Array.from({ length: 9 }, (_, offset) => ({
        latitude: 37 + offset * 0.0004,
        longitude: -122 + offset * 0.0006,
        time: 1700000000 + offset,
        monotonic: anchor + offset,
      }));
      m.engagement = recordedEngagement
        ? [
            { time: anchor, enabled: false, active: false },
            { time: anchor + 2, enabled: true, active: true },
            { time: anchor + 6, enabled: true, active: false },
          ]
        : [];
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
  await page
    .getByRole('textbox', { name: 'Playback timestamp' })
    .fill('0:03.125');
  await page.getByRole('button', { name: 'Go', exact: true }).click();
  await page.waitForFunction(
    () => Math.abs(document.querySelector('video').currentTime - 3.125) < 0.001,
  );
  assert.match(
    (await page.locator('.timestamp-button').innerText()).replace(/\s+/g, ' '),
    /0:03\.125 \/ \d+:\d{2}\.\d{3}/,
  );
  await page.getByRole('button', { name: 'Enter playback timestamp' }).click();
  assert.equal(
    await page
      .getByRole('textbox', { name: 'Playback timestamp' })
      .inputValue(),
    '0:03.125',
  );
  await page.keyboard.press('Escape');
  await page
    .getByRole('button', { name: 'Play recording', exact: true })
    .click();
  const samples = await page.evaluate(async () => {
    const values = new Set();
    const started = performance.now();
    while (performance.now() - started < 650) {
      await new Promise(requestAnimationFrame);
      values.add(document.querySelector('.video-timeline input').value);
    }
    return values.size;
  });
  assert(
    samples > 8,
    `Playback should update smoothly between timeupdate events, got ${samples} positions`,
  );
  await page
    .getByRole('button', { name: 'Pause recording', exact: true })
    .click();
  const stopped = await page.locator('.timestamp-button').innerText();
  await page.waitForTimeout(120);
  assert.equal(await page.locator('.timestamp-button').innerText(), stopped);
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
  assert.equal(await slider.getAttribute('step'), '0.001');
  await slider.fill('12.345');
  await page.waitForFunction(
    () =>
      Math.abs(document.querySelector('video').currentTime - 12.345) < 0.001,
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
  await page.locator('.playback-map-marker').first().waitFor();
  await page.getByRole('button', { name: 'Full route', exact: true }).click();
  await page
    .getByRole('button', { name: 'Play recording', exact: true })
    .click();
  const movement = await page.evaluate(async () => {
    const values = new Set();
    const start = performance.now();
    while (performance.now() - start < 650) {
      await new Promise(requestAnimationFrame);
      const marker = document.querySelector('.playback-map-marker');
      values.add(marker?.style.transform);
    }
    return values.size;
  });
  assert(
    movement > 8,
    `GPS marker must interpolate between native timeupdate events; got ${movement} positions`,
  );
  await page.getByRole('button', { name: 'Follow', exact: true }).click();
  const followingMovement = await page.evaluate(async () => {
    const values = new Set();
    const start = performance.now();
    const marker = document.querySelector('.playback-map-marker');
    while (performance.now() - start < 650) {
      await new Promise(requestAnimationFrame);
      values.add(marker?.style.transform);
    }
    // MapLibre keeps its DOM marker centered while the camera follows.
    const bounds = marker.getBoundingClientRect();
    const map = document.querySelector('.map-canvas').getBoundingClientRect();
    return {
      updates: 0,
      markerBounds: bounds.toJSON(),
      mapBounds: map.toJSON(),
      transform: marker.style.transform,
      centered:
        Math.abs(bounds.x + bounds.width / 2 - (map.x + map.width / 2)) < 3 &&
        Math.abs(bounds.y + bounds.height / 2 - (map.y + map.height / 2)) < 3,
    };
  });
  assert(
    followingMovement.centered,
    `Follow must center the moving playback marker: ${JSON.stringify(followingMovement)}`,
  );
  if (followingMovement.updates)
    assert(
      followingMovement.updates > 8,
      'Follow camera must pan between timeupdate events',
    );
  await page
    .getByRole('button', { name: 'Pause recording', exact: true })
    .click();
  await page.waitForTimeout(200);
  // Tiles can finish loading while paused; compare only the position geometry.
  const markerGeometry = () =>
    page.locator('.playback-map-marker').evaluate((e) => e.style.transform);
  const pausedPosition = await markerGeometry();
  const nextIndex = page.waitForResponse(
    (response) => new URL(response.url()).pathname === '/api/index',
  );
  await nextIndex;
  // Sample across the next summary poll, not merely between media events.
  await page.waitForTimeout(150);
  assert.equal(
    await markerGeometry(),
    pausedPosition,
    'Paused maps must not keep moving',
  );
  // Controlled visibility exercises the clock and renderer cleanup path.
  await page
    .getByRole('button', { name: 'Play recording', exact: true })
    .click();
  await page.evaluate(() => {
    Object.defineProperty(document, 'hidden', {
      configurable: true,
      value: true,
    });
    document.dispatchEvent(new Event('visibilitychange'));
  });
  const hiddenPosition = await markerGeometry();
  await page.waitForTimeout(400);
  assert.equal(
    await markerGeometry(),
    hiddenPosition,
    'Hidden maps must skip native as well as frame-driven updates',
  );
  await page.evaluate(() => {
    delete document.hidden;
    document.dispatchEvent(new Event('visibilitychange'));
  });
  await page
    .getByRole('button', { name: 'Pause recording', exact: true })
    .click();
  await slider.fill('3');
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
    [390, 844, 'dark'],
  ]) {
    if (size[2]) await page.emulateMedia({ colorScheme: size[2] });
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
    assert(
      await page
        .locator('.playback-toolbar')
        .evaluate((e) => e.scrollWidth <= e.clientWidth),
      'Playback controls must fit',
    );
    if (size[0] === 390) {
      const timer = await page.locator('.timestamp-button').boundingBox();
      const options = await page
        .getByRole('button', { name: 'Viewer options', exact: true })
        .boundingBox();
      assert(
        Math.abs(timer.y + timer.height / 2 - options.y - options.height / 2) <
          2,
        'Phone controls should share one row',
      );
    }
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
      path: `.artifacts/enhanced-viewer-${size[0]}${size[2] ? '-dark' : ''}.png`,
    });
  }
  // A stored "show engagement" preference must never turn absent data into
  // an enabled button or a pretend status row on another recording.
  recordedEngagement = false;
  await page.reload();
  await page.locator('video').waitFor({ state: 'attached' });
  if (
    !(await page
      .getByRole('button', { name: 'Viewer options', exact: true })
      .isVisible())
  )
    await page.locator('.drive-row').first().click();
  await page
    .getByRole('button', { name: 'Viewer options', exact: true })
    .click();
  assert(
    await page
      .getByRole('button', { name: 'Engagement not recorded', exact: true })
      .isDisabled(),
  );
  assert.equal(await page.locator('.engagement-row').count(), 0);
  assert.equal(await page.locator('.viewer-toolbar').count(), 0);
  assert.equal(
    await page
      .locator('.playback-toolbar')
      .getByRole('combobox', { name: 'Camera', exact: true })
      .count(),
    1,
  );
  assert.deepEqual(errors, []);
  console.log(
    'Enhanced viewer passed: sidebar drag/persistence/animated reset, padding, millisecond typed/slider seeking, smooth playback clock, speed, interpolated map motion/follow/full-route/pause/visibility, snapshots/engagement, responsive fit and accessibility.',
  );
} finally {
  await browser.close();
  await fetch(`${origin}/api/simulation/state`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ state: 'unknown' }),
  });
}
