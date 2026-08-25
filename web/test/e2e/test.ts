import { expect, test as base } from '@playwright/test';
import { installLoopbackNetworkBoundary } from './support';

interface FoundationFixtures {
  loopbackNetworkBoundary: void;
}

export const test = base.extend<FoundationFixtures>({
  loopbackNetworkBoundary: [
    async ({ context }, use) => {
      await installLoopbackNetworkBoundary(context);
      await use();
    },
    { auto: true },
  ],
});

export { expect };
