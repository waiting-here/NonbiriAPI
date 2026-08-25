import { defineConfig, devices } from '@playwright/test';
import { FIXTURE_ORIGIN } from './test/e2e/ports';

export default defineConfig({
  testDir: './test/e2e',
  testMatch: '**/*.spec.ts',
  fullyParallel: false,
  workers: 1,
  retries: 0,
  maxFailures: 1,
  timeout: 30_000,
  expect: { timeout: 5_000 },
  forbidOnly: Boolean(process.env.CI),
  reporter: [['line']],
  outputDir: 'test-results/playwright',
  preserveOutput: 'never',
  globalSetup: './test/e2e/global-setup.mjs',
  use: {
    baseURL: FIXTURE_ORIGIN,
    headless: true,
    serviceWorkers: 'block',
    trace: 'off',
    screenshot: 'off',
    video: 'off',
  },
  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'], browserName: 'chromium' },
    },
  ],
});
