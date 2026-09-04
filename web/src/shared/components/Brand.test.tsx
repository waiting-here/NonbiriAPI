import { cleanup, render } from '@testing-library/react';
import { afterEach, describe, expect, test } from 'vitest';
import { Brand, isPublicHTTPSURL, useBrandFavicon } from './Brand';

afterEach(() => {
  cleanup();
  document.head.querySelectorAll('link[data-brand-test]').forEach((node) => node.remove());
});

describe('brand asset boundary', () => {
  test('accepts only credential-free public HTTPS logos', () => {
    expect(isPublicHTTPSURL('https://cdn.example.test/logo.svg')).toBe(true);
    expect(isPublicHTTPSURL('https://app.example.test/logo.svg', 'https://app.example.test')).toBe(false);
    expect(isPublicHTTPSURL('http://cdn.example.test/logo.svg')).toBe(false);
    expect(isPublicHTTPSURL('data:image/svg+xml,broken')).toBe(false);
    expect(isPublicHTTPSURL('https://user:password@cdn.example.test/logo.svg')).toBe(false);
  });

  test('sets anonymous policy before the remote image source', () => {
    const rendered = render(<Brand siteName="Example" siteLogoURL="https://cdn.example.test/logo.svg" />);
    const logo = rendered.container.querySelector('img');
    if (!logo) throw new Error('brand image not rendered');
    expect(logo.getAttribute('crossorigin')).toBe('anonymous');
    expect(logo.getAttribute('referrerpolicy')).toBe('no-referrer');
    expect(logo.getAttribute('src')).toBe('https://cdn.example.test/logo.svg');
  });

  test('falls back to the local favicon and clears remote policy on failure', () => {
    const link = document.createElement('link');
    link.rel = 'icon';
    link.dataset.brandTest = 'true';
    document.head.append(link);

    function Favicon() {
      useBrandFavicon('https://cdn.example.test/favicon.svg');
      return null;
    }

    render(<Favicon />);
    expect(link.getAttribute('crossorigin')).toBe('anonymous');
    expect(link.getAttribute('referrerpolicy')).toBe('no-referrer');
    link.dispatchEvent(new Event('error'));
    expect(link.getAttribute('crossorigin')).toBeNull();
    expect(link.getAttribute('referrerpolicy')).toBeNull();
    expect(link.href).not.toContain('cdn.example.test');
  });
});
