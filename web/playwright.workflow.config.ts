import { resolve } from 'node:path';
import { defineConfig } from '@playwright/test';
import audit from './playwright.audit.config';

process.env.NONBIRI_MANAGEMENT_BROWSER = '1';
process.env.NONBIRI_WORKFLOW_BROWSER = '1';
process.env.NONBIRI_AUDIT_BROWSER_STATE = resolve('test-results/workflow/state.json');

export default defineConfig({
  ...audit,
  testMatch: ['workflow.spec.ts'],
  outputDir: 'test-results/workflow/browser',
});
