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

// jsdom does not implement the browser's modal dialog lifecycle.
Object.defineProperties(HTMLDialogElement.prototype, {
  showModal: {
    configurable: true,
    value(this: HTMLDialogElement) {
      this.open = true;
    },
  },
  close: {
    configurable: true,
    value(this: HTMLDialogElement) {
      this.open = false;
      this.dispatchEvent(new Event('close'));
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
