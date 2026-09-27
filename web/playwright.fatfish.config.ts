import { resolve } from 'node:path';
import { defineConfig, devices } from '@playwright/test';

process.env.NONBIRI_FISH_BROWSER_STATE = resolve('test-results/fatfish/state.json');

export default defineConfig({
  testDir: './test/fatfish',
  testMatch: '*.spec.ts',
  fullyParallel: false,
  workers: 1,
  retries: 0,
  maxFailures: 1,
  timeout: 180_000,
  expect: { timeout: 15_000 },
  forbidOnly: Boolean(process.env.CI),
  reporter: [['line']],
  outputDir: 'test-results/fatfish/browser',
  preserveOutput: 'failures-only',
  globalSetup: './test/fatfish/global-setup.mjs',
  use: {
    actionTimeout: 15_000,
    headless: true,
    serviceWorkers: 'block',
    trace: 'off',
    screenshot: 'only-on-failure',
    video: 'off',
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
});
