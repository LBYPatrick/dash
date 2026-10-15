import { defineConfig } from 'vitest/config';

// Run component logic and shared release tests from the repository root.
export default defineConfig({
  test: { include: ['frontend/src/**/*.test.ts', 'scripts/**/*.test.mjs'] },
});
