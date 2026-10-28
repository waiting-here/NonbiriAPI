import { resolve } from 'node:path';
import { defineConfig } from '@playwright/test';
import audit from './playwright.audit.config';

process.env.NONBIRI_MANAGEMENT_BROWSER = '1';
process.env.NONBIRI_AUDIT_BROWSER_STATE = resolve('test-results/management/state.json');

export default defineConfig({
  ...audit,
  testMatch: [
    'account-export.spec.ts',
    'account-mfa.spec.ts',
    'management.spec.ts',
    'agent-management.spec.ts',
    'steward-permissions.spec.ts',
    'inference-configuration.spec.ts',
    'moderation.spec.ts',
    'directory-controls.spec.ts',
    'operation-controls.spec.ts',
    'discovery-errors.spec.ts',
    'smart-backoff.spec.ts',
    'resource-health.spec.ts',
    'donation-compatibility.spec.ts',
    'recurring-discounts.spec.ts',
    'charity-binding-batch.spec.ts',
    'connection-experience.spec.ts',
    'closeout-controls.spec.ts',
  ],
  outputDir: 'test-results/management/browser',
});
