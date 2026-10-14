import { chromium } from '@playwright/test';
import assert from 'node:assert/strict';
import { installMapFixture } from './map-fixture.mjs';

const origin = process.env.DASH_TEST_URL || 'http://127.0.0.1:8088';
const browser = await chromium.launch({
  executablePath:
    process.env.CHROME_PATH ||
    '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
  headless: true,
});
const errors = [];
const observed = [];
const curve = 'cubic-bezier(0.23, 1, 0.32, 1)';

// Capture actual CSS transitions, rather than treating declarations as evidence
// that an element really animated. All export requests below stay in the browser.
async function openPage({ reduced = false, performance = false } = {}) {
  const context = await browser.newContext({
    viewport: { width: 1440, height: 900 },
    reducedMotion: reduced ? 'reduce' : 'no-preference',
  });
  await installMapFixture(context);
  await context.addInitScript(
    ({ performance }) => {
      localStorage.setItem(
        'dash.appearance',
        JSON.stringify({ theme: 'light', performance }),
      );
      window.motionEvents = [];
      window.exportHandoffs = [];
      const click = HTMLAnchorElement.prototype.click;
      HTMLAnchorElement.prototype.click = function () {
        if (this.pathname.startsWith('/api/exports/')) {
          window.exportHandoffs.push({
            at: globalThis.performance.now(),
            beforeRecoveryLink: !document.querySelector('.panel a[download]'),
          });
        }
        return click.call(this);
      };
      document.addEventListener(
        'transitionrun',
        (event) => {
          const target = event.target;
          if (
            !target.classList.contains('task-feedback-enter-active') &&
            !target.classList.contains('task-feedback-leave-active')
          )
            return;
          const style = getComputedStyle(target);
          window.motionEvents.push({
            property: event.propertyName,
            duration: style.transitionDuration,
            curve: style.transitionTimingFunction,
            transform: style.transform,
          });
        },
        true,
      );
    },
    { performance },
  );
  await context.route('**/api/index', async (route) => {
    const response = await route.fetch();
    const data = await response.json();
    data.status.driving = 'parked';
    await route.fulfill({ response, json: data });
  });
  const page = await context.newPage();
  page.on('pageerror', (error) => errors.push(error.message));
  await page.goto(origin);
  await page.locator('.drive-row').first().waitFor();
  await page.locator('.session-layout').waitFor();
  return { page, context };
}

async function activate(page, selector, { keyboard = false } = {}) {
  if (keyboard) {
    await page.locator(selector).focus();
    await page.keyboard.press('Enter');
    await page.evaluate(() => new Promise(requestAnimationFrame));
    return;
  }
  return page.evaluate(
    async ({ selector, keyboard }) => {
      const element = document.querySelector(selector);
      assertElement(element, selector);
      if (keyboard) {
        element.dispatchEvent(
          new KeyboardEvent('keydown', {
            key: 'Enter',
            bubbles: true,
            cancelable: true,
          }),
        );
      } else {
        element.dispatchEvent(
          new PointerEvent('pointerdown', {
            pointerType: 'mouse',
            bubbles: true,
            isPrimary: true,
          }),
        );
        element.click();
      }
      await Promise.resolve();
      await new Promise(requestAnimationFrame);
      function assertElement(value, label) {
        if (!value) throw new Error(`Missing ${label}`);
      }
    },
    { selector, keyboard },
  );
}

async function menuState(page, selector) {
  return page.locator(selector).evaluate((element) => {
    const style = getComputedStyle(element);
    return {
      open: element.matches(':popover-open'),
      display: style.display,
      opacity: +style.opacity,
      transform: style.transform,
      origin: style.transformOrigin,
      width: element.offsetWidth,
      height: element.offsetHeight,
      duration: style.transitionDuration,
      animations: element.getAnimations().map((animation) => ({
        property: animation.transitionProperty,
        duration: animation.effect.getTiming().duration,
        curve: animation.effect.getTiming().easing,
      })),
    };
  });
}

async function openExport(page) {
  await activate(page, '[aria-label="Session actions"]');
  await page.getByRole('button', { name: 'Download ZIP', exact: true }).click();
  await page.getByRole('dialog', { name: 'Export recordings' }).waitFor();
  await page.waitForTimeout(340);
}

try {
  const { page, context } = await openPage();
  await openExport(page);
  await activate(page, '#export-format');
  const entered = await menuState(page, '.menulist:popover-open');
  assert(
    entered.animations.some(
      (a) => a.property === 'opacity' && a.duration === 180,
    ),
    JSON.stringify(entered),
  );
  assert(
    entered.animations.some(
      (a) => a.property === 'transform' && a.duration === 180,
    ),
  );
  assert(
    entered.animations.every((a) =>
      ['opacity', 'transform', 'display', 'overlay'].includes(a.property),
    ),
  );
  assert(
    entered.animations
      .filter((a) => ['opacity', 'transform'].includes(a.property))
      .every((a) => a.curve === curve),
  );
  const [originX, originY] = entered.origin.split(' ').map(parseFloat);
  assert(
    Math.abs(originX - entered.width / 2) < 1 && Math.abs(originY) < 0.1,
    'Selector should open from its trigger-facing top edge',
  );
  observed.push({ name: 'selector opening', ...entered });
  // Reverse repeatedly before completion. Final state must follow the last tap.
  await activate(page, '#export-format');
  await activate(page, '#export-format');
  await page.waitForTimeout(220);
  assert.equal(await page.locator('.menulist:popover-open').count(), 1);
  await activate(page, '#export-format');
  await page.waitForTimeout(170);
  assert.equal(
    (await menuState(page, '.menulist[aria-label="Format"]')).display,
    'none',
  );
  await page.getByRole('button', { name: 'Close export' }).click();
  await page.waitForTimeout(340);

  await activate(page, '[aria-label="Session actions"]');
  await page.waitForTimeout(210);
  await activate(page, '[aria-label="Session actions"]');
  const exiting = await menuState(page, '.detail-heading .session-action-menu');
  assert.notEqual(
    exiting.display,
    'none',
    'Exit must retain the top-layer menu',
  );
  assert(
    exiting.animations.some(
      (a) => a.property === 'opacity' && a.duration === 140,
    ),
    JSON.stringify(exiting),
  );
  observed.push({ name: 'actions exit', ...exiting });
  await page.waitForTimeout(180);
  assert.equal(
    (await menuState(page, '.detail-heading .session-action-menu')).display,
    'none',
  );

  // The divider must start a new drag at the currently painted width, even if
  // the previous double-click reset has not reached its destination.
  const divider = page.getByRole('separator', {
    name: 'Resize video and map',
    exact: true,
  });
  await divider.focus();
  await page.keyboard.press('Home');
  await page.evaluate(() => {
    const handle = document.querySelector('.media-resize-handle');
    document.documentElement.dataset.interaction = 'pointer';
    handle.dispatchEvent(new MouseEvent('dblclick', { bubbles: true }));
    handle.addEventListener(
      'pointerdown',
      () => {
        window.grabBefore = document
          .querySelector('.session-video')
          .getBoundingClientRect().width;
        queueMicrotask(() =>
          queueMicrotask(() => {
            window.grabAfter = document
              .querySelector('.session-video')
              .getBoundingClientRect().width;
          }),
        );
      },
      { capture: true, once: true },
    );
  });
  await page.waitForTimeout(40);
  // Hold the CSS timeline at its intermediate frame while moving the physical
  // pointer to a divider that would otherwise move between protocol commands.
  await page
    .locator('.session-layout')
    .evaluate((e) => e.getAnimations().forEach((a) => a.pause()));
  await page.waitForTimeout(20);
  const grip = await divider.boundingBox();
  await page.mouse.move(grip.x + grip.width / 2, grip.y + grip.height / 2);
  await page.mouse.down();
  const grab = await page.evaluate(() => ({
    before: window.grabBefore,
    after: window.grabAfter,
  }));
  assert(
    Math.abs(grab.before - grab.after) <= 2,
    `Divider jumped: ${JSON.stringify(grab)}`,
  );
  await page.mouse.up();
  observed.push({ name: 'divider interrupted reset', ...grab });

  const sidebar = page.getByRole('separator', {
    name: 'Resize recording sidebar',
    exact: true,
  });
  await sidebar.focus();
  await page.keyboard.press('End');
  await page.keyboard.press('Enter');
  assert.equal(
    await sidebar.evaluate((e) => getComputedStyle(e).transitionProperty),
    'none',
  );
  assert.equal(
    await page
      .locator('.workspace')
      .evaluate((e) => getComputedStyle(e).transitionProperty),
    'none',
  );
  await context.close();

  for (const mode of ['reduced', 'performance', 'keyboard']) {
    const { page, context } = await openPage({
      reduced: mode === 'reduced',
      performance: mode === 'performance',
    });
    await openExport(page);
    await activate(page, '#export-format', { keyboard: mode === 'keyboard' });
    const state = await menuState(page, '.menulist:popover-open');
    assert.equal(
      state.animations.length,
      0,
      `${mode} selector should open immediately: ${JSON.stringify(state)}`,
    );
    assert.equal(state.transform, 'none');
    await page.keyboard.press('Escape');
    await page.getByRole('button', { name: 'Close export' }).click();
    await page.waitForTimeout(340);
    await activate(page, '[aria-label="Session actions"]', {
      keyboard: mode === 'keyboard',
    });
    const actions = await menuState(
      page,
      '.detail-heading .session-action-menu',
    );
    assert(actions.open, `${mode} actions must actually be open`);
    assert.equal(
      actions.animations.length,
      0,
      `${mode} actions should open immediately`,
    );
    if (mode === 'performance') {
      const handle = page.getByRole('separator', {
        name: 'Resize recording sidebar',
        exact: true,
      });
      await page.keyboard.press('Escape');
      await handle.focus();
      await page.keyboard.press('End');
      await handle.dblclick();
      assert.equal(
        await handle.evaluate((e) => getComputedStyle(e).transitionProperty),
        'none',
      );
      assert.equal(
        await page
          .locator('.workspace')
          .evaluate((e) => getComputedStyle(e).transitionProperty),
        'none',
      );
    }
    observed.push({ name: `${mode} menus immediate` });
    await context.close();
  }

  for (const mode of ['normal', 'reduced', 'performance']) {
    const { page, context } = await openPage({
      reduced: mode === 'reduced',
      performance: mode === 'performance',
    });
    let complete;
    const completion = new Promise((resolve) => {
      complete = resolve;
    });
    await context.route('**/api/exports', async (route) => {
      assert.equal(route.request().method(), 'POST');
      await completion;
      await route.fulfill({
        json: {
          id: 'motion-fixture',
          status: 'complete',
          name: 'motion-fixture.zip',
        },
      });
    });
    await context.route('**/api/exports/motion-fixture/download', (route) =>
      route.fulfill({
        body: 'Synthetic export; no device job was created.',
        contentType: 'application/zip',
        headers: {
          'content-disposition': 'attachment; filename="motion-fixture.zip"',
        },
      }),
    );
    await openExport(page);
    await page
      .getByRole('button', { name: 'Download recording', exact: true })
      .click();
    await page
      .getByRole('button', { name: 'Preparing download…', exact: true })
      .waitFor();
    const download = page.waitForEvent('download');
    complete();
    await download;
    await page
      .getByRole('link', { name: 'Download again', exact: true })
      .waitFor();
    await page.waitForTimeout(200);
    const result = await page.evaluate(() => ({
      handoffs: window.exportHandoffs,
      transitions: window.motionEvents,
    }));
    assert.equal(result.handoffs.length, 1);
    assert(
      result.handoffs[0].beforeRecoveryLink,
      'Automatic handoff must occur before the recovery-link transition',
    );
    if (mode === 'performance') assert.equal(result.transitions.length, 0);
    else {
      assert(
        result.transitions.length >= 2,
        `${mode}: missing actual task fades ${JSON.stringify(result)}`,
      );
      assert(
        result.transitions.every(
          (t) =>
            t.property === 'opacity' &&
            t.transform === 'none' &&
            t.curve === curve,
        ),
      );
      assert(
        result.transitions.every(
          (t) => t.duration === (mode === 'reduced' ? '0.12s' : '0.14s'),
        ),
        JSON.stringify(result),
      );
    }
    observed.push({
      name: `${mode} task feedback and immediate download`,
      ...result,
    });
    await context.close();
  }
  assert.deepEqual(errors, []);
  console.log(JSON.stringify(observed, null, 2));
  console.log(
    'Motion passed: real menu entry/exit, rapid reversal, trigger origins, reduced/performance/keyboard handling, divider interruption, sidebar reset, opacity-only task feedback, immediate mocked export download. No device state or processing jobs changed.',
  );
} finally {
  await browser.close();
}
