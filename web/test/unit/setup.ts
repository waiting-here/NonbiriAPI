import '@testing-library/jest-dom/vitest';
import { cleanup } from '@testing-library/react';
import { afterEach } from 'vitest';
import { disposeTestProviders } from './support';

// jsdom does not implement the browser's popover top layer.
Object.defineProperties(HTMLElement.prototype, {
  showPopover: {
    configurable: true,
    value(this: HTMLElement) {
      this.style.display = 'grid';
    },
  },
  hidePopover: {
    configurable: true,
    value(this: HTMLElement) {
      this.style.display = 'none';
    },
  },
});

afterEach(async () => {
  cleanup();
  await disposeTestProviders();
  window.localStorage.clear();
  window.sessionStorage.clear();
  document.documentElement.lang = '';
  delete document.documentElement.dataset.theme;
});
