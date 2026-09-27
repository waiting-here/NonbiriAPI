import { defineConfig } from 'vitest/config';

export default defineConfig({
  test: {
    environment: 'node',
    include: ['test/fatfish-assets/*.test.ts'],
    testTimeout: 30_000,
  },
});
