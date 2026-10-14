// Browser-only signaling/recorder doubles prove route retention. They do not
// contact firmware, record the user's camera, or create backend jobs.
import { chromium } from '@playwright/test';
import assert from 'node:assert/strict';
import { installMapFixture } from './map-fixture.mjs';
const browser = await chromium.launch({
  executablePath:
    process.env.CHROME_PATH ||
    '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
  headless: true,
});
try {
  const context = await browser.newContext({
    viewport: { width: 390, height: 844 },
    hasTouch: true,
    isMobile: true,
  });
  await installMapFixture(context);
  await context.route('**/api/index', async (route) => {
    const response = await route.fetch();
    const data = await response.json();
    data.status.driving = 'parked';
    await route.fulfill({ response, json: data });
  });
  await context.route('**/api/stream**', (route) =>
    route.fulfill({ json: { sdp: 'test answer', driving: 'parked' } }),
  );
  await context.addInitScript(() => {
    window.__stoppedTracks = 0;
    window.__closedPeers = 0;
    const canvas = document.createElement('canvas');
    canvas.width = 16;
    canvas.height = 16;
    class Peer {
      iceGatheringState = 'complete';
      connectionState = 'new';
      localDescription = { sdp: 'test offer' };
      addTransceiver() {}
      createDataChannel() {}
      async createOffer() {
        return { type: 'offer', sdp: 'test offer' };
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
        this.connectionState = 'connected';
        this.onconnectionstatechange?.();
      }
      close() {
        window.__closedPeers++;
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
        }, 30);
      }
    }
    window.RTCPeerConnection = Peer;
    window.MediaRecorder = Recorder;
  });
  const page = await context.newPage();
  await page.goto(process.env.DASH_TEST_URL || 'http://127.0.0.1:8088');
  await page.locator('.drive-row').first().waitFor();
  const navigate = async (name) => {
    await page
      .getByRole('button', { name: 'Open navigation', exact: true })
      .click();
    await page
      .getByRole('dialog', { name: 'Navigation', exact: true })
      .getByRole('button', { name, exact: true })
      .click();
  };
  await navigate('Live');
  await page
    .getByRole('button', { name: 'Start live view', exact: true })
    .click();
  await page
    .getByRole('button', { name: 'Record this feed', exact: true })
    .waitFor({ state: 'visible' });
  await page
    .getByRole('button', { name: 'Record this feed', exact: true })
    .click();
  await page
    .getByRole('button', { name: 'Stop recording', exact: true })
    .waitFor();
  await navigate('Recordings');
  await page.locator('.drive-row').first().waitFor({ state: 'visible' });
  await page.waitForTimeout(150);
  assert(
    await page.evaluate(
      () => window.__closedPeers > 0 && window.__stoppedTracks > 0,
    ),
    'Leaving Live stops peer and tracks',
  );
  await navigate('Live');
  const save = page.getByRole('button', {
    name: 'Save recording',
    exact: true,
  });
  await save.waitFor();
  const download = page.waitForEvent('download');
  await save.click();
  const file = await download;
  assert.match(file.suggestedFilename(), /^dash-live-.*\.webm$/);
  await navigate('Overview');
  await navigate('Live');
  assert(
    await save.isVisible(),
    'Finalized clip survives repeated navigation and remains downloadable',
  );
  await page
    .getByRole('button', { name: 'Start live view', exact: true })
    .click();
  await page
    .getByRole('button', { name: 'Record new clip', exact: true })
    .click();
  await page
    .getByRole('button', { name: 'Stop recording', exact: true })
    .waitFor();
  await page.evaluate(() => {
    Object.defineProperty(document, 'hidden', {
      configurable: true,
      value: true,
    });
    document.dispatchEvent(new Event('visibilitychange'));
  });
  await page
    .getByText('Live view stopped when Dash moved to the background.', {
      exact: true,
    })
    .first()
    .waitFor();
  await save.waitFor();
  assert(
    await page.evaluate(
      () => window.__closedPeers >= 2 && window.__stoppedTracks >= 2,
    ),
    'Backgrounding stops peer/tracks and finalizes a new clip',
  );
  await page.evaluate(() => {
    delete document.hidden;
    document.dispatchEvent(new Event('visibilitychange'));
  });
  console.log(
    'Live capture passed: active recording finalizes on route leave and visibility loss; peer/tracks stop; clip survives view recreation and repeated navigation; download remains available. All media/signaling mocked, no car or user camera contacted.',
  );
  await context.close();
} finally {
  await browser.close();
}
