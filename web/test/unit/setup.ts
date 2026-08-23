import '@testing-library/jest-dom/vitest';
import { cleanup } from '@testing-library/react';
import { afterEach } from 'vitest';
import { disposeTestProviders } from './support';

afterEach(async () => {
  cleanup();
  await disposeTestProviders();
  window.localStorage.clear();
  window.sessionStorage.clear();
  document.documentElement.lang = '';
  delete document.documentElement.dataset.theme;
});
