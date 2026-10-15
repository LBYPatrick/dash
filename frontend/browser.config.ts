import { defineConfig } from 'vite-plus';

// Browser acceptance is opt-in and serial: it needs the synthetic backend and
// Chrome. Ordinary `vp test run` remains fast and does not launch a browser.
export default defineConfig({
  test: {
    include: ['frontend/tests/browser/**/*.test.js'],
    setupFiles: ['frontend/tests/browser/setup.js'],
    fileParallelism: false,
    maxWorkers: 1,
    testTimeout: 180_000,
    hookTimeout: 30_000,
  },
});
