import { resolve } from 'node:path';
import { defineConfig, devices } from '@playwright/test';

process.env.NONBIRI_IMAGE_BROWSER_STATE = resolve('test-results/picturebook/state.json');

export default defineConfig({
  testDir: './test/picturebook',
  testMatch: '*.spec.ts',
  fullyParallel: false,
  workers: 1,
  retries: 0,
  maxFailures: 1,
  timeout: 90_000,
  expect: { timeout: 15_000 },
  forbidOnly: Boolean(process.env.CI),
  reporter: [['line']],
  outputDir: 'test-results/picturebook/browser',
  preserveOutput: 'failures-only',
  globalSetup: './test/picturebook/global-setup.mjs',
  use: {
    headless: true,
    serviceWorkers: 'block',
    trace: 'off',
    screenshot: 'only-on-failure',
    video: 'off',
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
});
