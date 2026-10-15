import { chromium } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import { assert, test } from 'vite-plus/test';

test('live-layout-smoke', async () => {
  // Browser-only camera/signaling fixtures. This test never contacts a device.
  const browser = await chromium.launch({
    executablePath:
      process.env.CHROME_PATH ||
      '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
    headless: true,
    args: [
      '--use-gl=angle',
      '--use-angle=swiftshader',
      '--enable-unsafe-swiftshader',
      '--disable-accelerated-video-decode',
      '--renderer-process-limit=2',
    ],
  });
  try {
    const context = await browser.newContext({
      viewport: { width: 390, height: 844 },
      hasTouch: true,
      isMobile: true,
    });
    let driving = 'parked';
    let stopRequests = 0;
    await context.route('**/files/**', (route) =>
      route.fulfill({ json: { dirs: [], files: [] } }),
    );
    await context.route('**/api/**', (route) => {
      const path = new URL(route.request().url()).pathname;
      if (path === '/api/stream/stop') stopRequests++;
      return route.fulfill({
        json:
          path === '/api/info'
            ? { kind: 'dash-backend', version: 1 }
            : path === '/api/index'
              ? {
                  version: 1,
                  segments: {},
                  status: {
                    running: false,
                    completed: 0,
                    total: 0,
                    errors: 0,
                    driving,
                  },
                }
              : { sdp: 'fixture answer', driving, active: true },
      });
    });
    await context.addInitScript(() => {
      window.__stoppedTracks = 0;
      const canvas = document.createElement('canvas');
      canvas.width = 640;
      canvas.height = 400;
      const ctx = canvas.getContext('2d');
      ctx.fillStyle = '#607080';
      ctx.fillRect(0, 0, 640, 400);
      class Peer {
        iceGatheringState = 'complete';
        connectionState = 'new';
        localDescription = { sdp: 'fixture offer' };
        addTransceiver() {}
        createDataChannel() {}
        async createOffer() {
          return { type: 'offer', sdp: 'fixture offer' };
        }
        async setLocalDescription() {}
        async setRemoteDescription() {
          this.track = canvas.captureStream(1).getVideoTracks()[0];
          const stop = this.track.stop.bind(this.track);
          this.track.stop = () => {
            window.__stoppedTracks++;
            stop();
          };
          this.ontrack?.({ track: this.track });
          this.timer = setInterval(() => {
            ctx.fillRect(0, 0, 640, 400);
          }, 250);
          this.connectionState = 'connected';
          this.onconnectionstatechange?.();
        }
        close() {
          clearInterval(this.timer);
          this.connectionState = 'closed';
        }
      }
      class Recorder {
        static isTypeSupported() {
          return true;
        }
        state = 'inactive';
        constructor(stream, options) {
          this.mime = options.mimeType;
        }
        start() {
          this.state = 'recording';
        }
        stop() {
          if (this.state !== 'recording') return;
          this.state = 'inactive';
          setTimeout(() => {
            this.ondataavailable?.({
              data: new Blob(['synthetic clip'], { type: this.mime }),
            });
            this.onstop?.();
          }, 20);
        }
      }
      window.RTCPeerConnection = Peer;
      window.MediaRecorder = Recorder;
    });
    const page = await context.newPage();
    const errors = [];
    page.on('pageerror', (error) => errors.push(error.message));
    await page.goto(process.env.DASH_TEST_URL || 'http://127.0.0.1:8095');
    await page
      .getByRole('button', { name: 'Open navigation', exact: true })
      .click();
    await page
      .getByRole('dialog', { name: 'Navigation', exact: true })
      .getByRole('button', { name: 'Live', exact: true })
      .click();
    const start = page.getByRole('button', {
      name: 'Start live view',
      exact: true,
    });
    await start.waitFor();
    async function fits(width, height) {
      await page.setViewportSize({ width, height });
      await page.waitForTimeout(80);
      const geometry = await page.locator('.live-detail').evaluate((detail) => {
        const player = detail
          .querySelector('.live-player')
          .getBoundingClientRect();
        const controls = [
          ...detail.querySelectorAll('.live-controls button'),
        ].map((e) => {
          const rect = e.getBoundingClientRect();
          return {
            x: rect.x,
            right: rect.right,
            bottom: rect.bottom,
            height: rect.height,
            width: rect.width,
          };
        });
        return {
          height: detail.clientHeight,
          scroll: detail.scrollHeight,
          body: document.scrollingElement.scrollHeight,
          viewport: innerHeight,
          player: { width: player.width, height: player.height },
          styles: [
            '.live-detail',
            '.live-view',
            '.live-controls',
            '.live-player',
            '.workspace',
          ].map((s) => {
            const e = document.querySelector(s);
            const c = getComputedStyle(e);
            return {
              s,
              display: c.display,
              flex: c.flex,
              width: c.width,
              height: c.height,
              padding: c.padding,
              grid: c.gridTemplateColumns,
            };
          }),
          controls,
        };
      });
      assert(
        geometry.scroll <= geometry.height + 1,
        `Live must fit ${width}x${height}: ${JSON.stringify(geometry)}`,
      );
      assert(geometry.body <= geometry.viewport + 1, 'No document scroll');
      assert(geometry.player.height >= 72, 'The camera remains visible');
      for (const rect of geometry.controls) {
        assert(
          rect.bottom <= height + 1 && rect.x >= 0 && rect.right <= width + 1,
          'Every control remains onscreen',
        );
        assert(
          rect.height >= 44 && rect.width >= 44,
          `Touch targets are at least 44px at ${width}x${height}: ${JSON.stringify(geometry)}`,
        );
      }
      if (process.env.DASH_LIVE_SCREENSHOTS)
        await page.screenshot({
          path: `.artifacts/live-${width}x${height}.png`,
        });
    }
    for (const size of [
      [320, 568],
      [390, 650],
      [390, 844],
      [844, 390],
      [1280, 720],
      [1920, 1080],
    ])
      await fits(...size);
    await page.setViewportSize({ width: 390, height: 844 });
    await page
      .getByRole('button', { name: 'About live view', exact: true })
      .click();
    await page
      .getByRole('dialog', { name: 'About live view', exact: true })
      .waitFor();
    await page.getByRole('button', { name: 'Close help', exact: true }).click();
    await start.click();
    const record = page.getByRole('button', {
      name: 'Record this feed',
      exact: true,
    });
    await record.click();
    await page
      .getByRole('button', { name: 'Stop recording', exact: true })
      .waitFor();
    await page.waitForTimeout(1100);
    assert.match(
      await page.locator('.live-state time').innerText(),
      /^0:0[1-9]$/,
    );
    await page
      .getByRole('button', { name: 'Stop recording', exact: true })
      .click();
    const save = page.getByRole('button', {
      name: 'Save recording',
      exact: true,
    });
    await save.waitFor();
    for (const size of [
      [320, 568],
      [390, 650],
      [844, 390],
      [1280, 720],
    ])
      await fits(...size);
    for (const theme of ['light', 'dark']) {
      await page.evaluate(
        (theme) => (document.documentElement.dataset.theme = theme),
        theme,
      );
      const axe = await new AxeBuilder({ page })
        .include('.live-view')
        .withTags(['wcag2a', 'wcag2aa'])
        .analyze();
      assert.deepEqual(
        axe.violations,
        [],
        `Live accessibility ${theme}: ${JSON.stringify(axe.violations.map((v) => ({ id: v.id, nodes: v.nodes.map((n) => n.target) })))}`,
      );
    }
    const download = page.waitForEvent('download');
    await save.click();
    assert.match((await download).suggestedFilename(), /^dash-live-.*\.webm$/);
    driving = 'unknown';
    await start.waitFor({ timeout: 6000 });
    await page.waitForFunction(
      () => document.querySelector('.live-connect')?.disabled,
      { timeout: 6000 },
    );
    assert(await start.isDisabled(), 'Unknown state disables connect');
    assert(
      stopRequests > 0 &&
        (await page.evaluate(() => window.__stoppedTracks > 0)),
      'Unknown state stops peer and tracks',
    );
    assert(
      await save.isVisible(),
      'Recording remains downloadable after safety stop',
    );
    assert.deepEqual(errors, []);
    console.log(
      'Live layout passed: six viewport sizes without scrolling, reachable 44px controls, saved clip/timer, Shelf help sheet, light/dark WCAG checks, and unknown-state cleanup. All media/API mocked.',
    );
  } finally {
    await browser.close();
  }
});
