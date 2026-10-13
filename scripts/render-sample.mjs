import { chromium } from '@playwright/test';
const browser = await chromium.launch({
  executablePath:
    '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
  headless: true,
});
const page = await browser.newPage({ viewport: { width: 1200, height: 650 } });
await page.goto(`file://${process.cwd()}/public/sample-road.svg`);
await page.screenshot({ path: '.artifacts/sample-road.png' });
await browser.close();
