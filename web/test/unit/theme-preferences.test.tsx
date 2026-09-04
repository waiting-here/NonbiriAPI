import { act, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import i18n, {
  configureI18n,
  detectLanguage,
  LANGUAGE_STORAGE_KEY,
} from '@shared/i18n';
import { ThemeProvider } from '@shared/theme/ThemeProvider';
import {
  DENSITY_STORAGE_KEY,
  FONT_SIZE_STORAGE_KEY,
  THEME_STORAGE_KEY,
} from '@shared/theme/context';
import { useTheme } from '@shared/theme/useTheme';

const originalNavigatorLanguages = Object.getOwnPropertyDescriptor(navigator, 'languages');

function ThemeProbe() {
  const { theme, density, fontSize, setDensity, setFontSize } = useTheme();
  return (
    <div>
      <output data-testid="theme">{theme}</output>
      <output data-testid="density">{density}</output>
      <output data-testid="font-size">{fontSize}</output>
      <button type="button" onClick={() => setDensity('compact')}>
        Compact density
      </button>
      <button type="button" onClick={() => setFontSize('large')}>
        Large font
      </button>
    </div>
  );
}

function setBrowserLanguages(languages: readonly string[]) {
  Object.defineProperty(navigator, 'languages', {
    configurable: true,
    value: [...languages],
  });
}

beforeEach(() => {
  vi.stubGlobal('matchMedia', (query: string) => ({
    matches: query === '(prefers-color-scheme: dark)' ? false : false,
    media: query,
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
  }));
});

afterEach(() => {
  if (originalNavigatorLanguages) {
    Object.defineProperty(navigator, 'languages', originalNavigatorLanguages);
  } else {
    Reflect.deleteProperty(navigator, 'languages');
  }
  delete document.documentElement.dataset.density;
  delete document.documentElement.dataset.fontSize;
  delete document.documentElement.dataset.theme;
});

describe('theme preference boundary', () => {
  test('falls back safely for invalid stored preferences', () => {
    window.localStorage.setItem(THEME_STORAGE_KEY, 'neon');
    window.localStorage.setItem(DENSITY_STORAGE_KEY, 'spacious');
    window.localStorage.setItem(FONT_SIZE_STORAGE_KEY, 'giant');

    render(
      <ThemeProvider>
        <ThemeProbe />
      </ThemeProvider>,
    );

    expect(screen.getByTestId('theme')).toHaveTextContent('system');
    expect(screen.getByTestId('density')).toHaveTextContent('comfortable');
    expect(screen.getByTestId('font-size')).toHaveTextContent('default');
    expect(document.documentElement.dataset.theme).toBe('light');
    expect(document.documentElement.dataset.density).toBe('comfortable');
    expect(document.documentElement.dataset.fontSize).toBe('default');
  });

  test('falls back safely when preference storage is blocked', () => {
    vi.spyOn(window.localStorage, 'getItem').mockImplementation(() => {
      throw new DOMException('blocked', 'SecurityError');
    });
    vi.spyOn(window.localStorage, 'setItem').mockImplementation(() => {
      throw new DOMException('blocked', 'SecurityError');
    });

    expect(() =>
      render(
        <ThemeProvider>
          <ThemeProbe />
        </ThemeProvider>,
      ),
    ).not.toThrow();
    expect(screen.getByTestId('density')).toHaveTextContent('comfortable');
    expect(screen.getByTestId('font-size')).toHaveTextContent('default');
    expect(document.documentElement.dataset.density).toBe('comfortable');
    expect(document.documentElement.dataset.fontSize).toBe('default');
  });

  test('controlled density and font setters persist and expose data attributes', async () => {
    const user = userEvent.setup();
    render(
      <ThemeProvider>
        <ThemeProbe />
      </ThemeProvider>,
    );

    await user.click(screen.getByRole('button', { name: 'Compact density' }));
    await user.click(screen.getByRole('button', { name: 'Large font' }));

    expect(screen.getByTestId('density')).toHaveTextContent('compact');
    expect(screen.getByTestId('font-size')).toHaveTextContent('large');
    expect(document.documentElement.dataset.density).toBe('compact');
    expect(document.documentElement.dataset.fontSize).toBe('large');
    expect(window.localStorage.getItem(DENSITY_STORAGE_KEY)).toBe('compact');
    expect(window.localStorage.getItem(FONT_SIZE_STORAGE_KEY)).toBe('large');
  });
});

describe('language preference detection', () => {
  test('uses explicit supported language before browser languages', () => {
    setBrowserLanguages(['zh-CN', 'en-US']);
    window.localStorage.setItem(LANGUAGE_STORAGE_KEY, 'en');
    expect(detectLanguage()).toBe('en');

    window.localStorage.setItem(LANGUAGE_STORAGE_KEY, 'zh');
    setBrowserLanguages(['en-US']);
    expect(detectLanguage()).toBe('zh');
  });

  test('uses zh when any navigator language is zh and otherwise falls back to en', () => {
    window.localStorage.setItem(LANGUAGE_STORAGE_KEY, 'unsupported');
    setBrowserLanguages(['fr-FR', 'zh-Hant-TW', 'de-DE']);
    expect(detectLanguage()).toBe('zh');

    setBrowserLanguages(['fr-FR', 'de-DE']);
    expect(detectLanguage()).toBe('en');
  });

  test('synchronizes the document language on initial detection and changes', async () => {
    window.localStorage.setItem(LANGUAGE_STORAGE_KEY, 'zh');
    setBrowserLanguages(['en-US']);
    configureI18n({}, {});

    expect(document.documentElement.lang).toBe('zh-CN');
    await act(async () => {
      await i18n.changeLanguage('en');
    });
    expect(document.documentElement.lang).toBe('en');
  });
});
