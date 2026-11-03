import { resolve } from 'node:path';
import { defineConfig } from '@playwright/test';
import audit from './playwright.audit.config';

// Advancing the site calendar must not change other management scenarios.
process.env.NONBIRI_MANAGEMENT_BROWSER = '1';
process.env.NONBIRI_V136_BROWSER = '1';
process.env.NONBIRI_AUDIT_BROWSER_STATE = resolve('test-results/attendance/state.json');

export default defineConfig({
  ...audit,
  testMatch: 'attendance-health.spec.ts',
  outputDir: 'test-results/attendance/browser',
});
