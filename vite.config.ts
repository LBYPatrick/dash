import { defineConfig } from 'vite-plus';

export default defineConfig({
  defaultPackage: './frontend',
  test: {
    include: ['frontend/src/**/*.test.ts', 'frontend/tests/unit/**/*.test.js'],
  },
  lint: {
    plugins: ['typescript', 'vue'],
    categories: { correctness: 'error' },
    ignorePatterns: ['**/dist/**', '.artifacts/**', 'frontend/src/vendor/**'],
    jsPlugins: [{ name: 'vite-plus', specifier: 'vite-plus/oxlint-plugin' }],
    rules: { 'vite-plus/prefer-vite-plus-imports': 'error' },
    options: { typeAware: true, typeCheck: true, denyWarnings: true },
  },
  fmt: {
    singleQuote: true,
    semi: true,
    trailingComma: 'all',
    printWidth: 80,
    sortPackageJson: false,
    ignorePatterns: [
      'node_modules',
      'dist',
      '.artifacts',
      'pnpm-lock.yaml',
      '**/dist/**',
      '**/.env',
      '**/.env.*',
      'backend/go.sum',
      'backend/internal/telemetry/testdata',
    ],
  },
});
