import { resolve } from 'node:path';
import { defineConfig } from '@playwright/test';
import audit from './playwright.audit.config';

process.env.NONBIRI_MANAGEMENT_BROWSER = '1';
process.env.NONBIRI_AUDIT_BROWSER_STATE = resolve('test-results/management/state.json');

export default defineConfig({
  ...audit,
  testMatch: 'management.spec.ts',
  outputDir: 'test-results/management/browser',
});
