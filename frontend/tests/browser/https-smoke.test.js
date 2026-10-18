import { chromium } from '@playwright/test';
import { createHash, X509Certificate } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { assert, test } from 'vite-plus/test';

// Optional exact-certificate pinning is confined to this test browser. It does
// not disable certificate errors globally or change the user's OS trust store.
test.skipIf(process.env.DASH_TLS_ACCEPTANCE !== '1')(
  'trusted HTTPS exposes WebCodecs and service workers',
  async () => {
    const origin = process.env.DASH_TEST_URL;
    assert(origin?.startsWith('https://'));
    const args = ['--disable-gpu', '--disable-accelerated-video-decode'];
    if (process.env.DASH_TLS_PIN_CERTIFICATE) {
      const certificate = new X509Certificate(
        readFileSync(process.env.DASH_TLS_PIN_CERTIFICATE),
      );
      const pin = createHash('sha256')
        .update(certificate.publicKey.export({ type: 'spki', format: 'der' }))
        .digest('base64');
      args.push(`--ignore-certificate-errors-spki-list=${pin}`);
    }
    const browser = await chromium.launch({
      executablePath: process.env.CHROME_PATH,
      headless: true,
      args,
    });
    try {
      const page = await browser.newPage();
      const errors = [];
      page.on('pageerror', (error) => errors.push(error.message));
      const response = await page.goto(origin);
      assert.equal(response.status(), 200);
      assert.equal(new URL(page.url()).origin, new URL(origin).origin);
      await page.waitForFunction(() => document.querySelector('main'));
      const capabilities = await page.evaluate(async () => {
        const config = {
          codec: 'avc1.42001E',
          width: 256,
          height: 144,
          bitrate: 250000,
          framerate: 20,
          hardwareAcceleration: 'prefer-software',
          avc: { format: 'annexb' },
        };
        const result = {
          secure: isSecureContext,
          encoder: typeof VideoEncoder,
          decoder: typeof VideoDecoder,
          serviceWorker: 'serviceWorker' in navigator,
        };
        const support = await VideoEncoder.isConfigSupported(config);
        result.h264Encoder = support.supported;
        result.hevcDecoder = (
          await VideoDecoder.isConfigSupported({ codec: 'hev1.1.6.L93.B0' })
        ).supported;
        result.hevcEncoder = (
          await VideoEncoder.isConfigSupported({
            ...config,
            codec: 'hvc1.1.6.L93.B0',
          })
        ).supported;
        const chunks = [],
          failures = [];
        const encoder = new VideoEncoder({
          output: (chunk) => chunks.push(chunk),
          error: (error) => failures.push(error.message),
        });
        const decoder = new VideoDecoder({
          output: (frame) => {
            result.decodedFrames = (result.decodedFrames || 0) + 1;
            frame.close();
          },
          error: (error) => failures.push(error.message),
        });
        try {
          encoder.configure(config);
          const canvas = document.createElement('canvas');
          canvas.width = 256;
          canvas.height = 144;
          canvas.getContext('2d').fillRect(0, 0, 256, 144);
          for (let index = 0; index < 3; index++) {
            const frame = new VideoFrame(canvas, {
              timestamp: index * 50000,
              duration: 50000,
            });
            encoder.encode(frame, { keyFrame: index === 0 });
            frame.close();
          }
          await encoder.flush();
          decoder.configure({ codec: config.codec });
          for (const chunk of chunks) decoder.decode(chunk);
          await decoder.flush();
          result.encodedFrames = chunks.length;
          result.failures = failures;
        } finally {
          encoder.close();
          decoder.close();
        }
        const api = await fetch('/api/info');
        result.apiStatus = api.status;
        const registration = await navigator.serviceWorker.ready;
        result.serviceWorkerActive = !!registration.active;
        return result;
      });
      console.info('HTTPS/WebCodecs capabilities:', capabilities);
      assert.equal(capabilities.secure, true);
      assert.equal(capabilities.encoder, 'function');
      assert.equal(capabilities.decoder, 'function');
      assert.equal(capabilities.encodedFrames, 3);
      assert.equal(capabilities.decodedFrames, 3);
      assert.equal(capabilities.serviceWorkerActive, true);
      assert.equal(capabilities.apiStatus, 200);
      assert.deepEqual(capabilities.failures, []);
      assert.deepEqual(errors, []);
    } finally {
      await browser.close();
    }
  },
);
