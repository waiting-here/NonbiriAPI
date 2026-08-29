import { useCallback, useEffect, useMemo, useState, type ReactNode } from 'react';
import {
  DENSITY_STORAGE_KEY,
  FONT_SIZE_STORAGE_KEY,
  THEME_STORAGE_KEY,
  ThemeContext,
  type Density,
  type FontSize,
  type Theme,
} from './context';

const DARK_QUERY = '(prefers-color-scheme: dark)';

function systemPrefersDark(): boolean {
  return typeof window !== 'undefined' && window.matchMedia(DARK_QUERY).matches;
}

// resolveTheme maps the user's choice to the concrete value the CSS tokens
// depend on. The data-theme attribute only ever carries 'light' or 'dark';
// 'system' is resolved against the OS preference so it can react live.
function resolveTheme(theme: Theme): 'light' | 'dark' {
  return theme === 'system' ? (systemPrefersDark() ? 'dark' : 'light') : theme;
}

function getInitialTheme(): Theme {
  let stored: string | null = null;
  try {
    stored = typeof window !== 'undefined' ? window.localStorage.getItem(THEME_STORAGE_KEY) : null;
  } catch {
    stored = null;
  }
  const theme: Theme =
    stored === 'light' || stored === 'dark' || stored === 'system' ? stored : 'system';
  // The embedded station shell uses a strict script CSP, so apply the initial
  // resolved theme from module code before the first React paint instead of
  // relying on an inline HTML bootstrap script.
  if (typeof document !== 'undefined') document.documentElement.dataset.theme = resolveTheme(theme);
  return theme;
}

function readDensity(): Density {
  try {
    const stored = typeof window !== 'undefined' ? window.localStorage.getItem(DENSITY_STORAGE_KEY) : null;
    return stored === 'compact' ? 'compact' : 'comfortable';
  } catch {
    return 'comfortable';
  }
}

function readFontSize(): FontSize {
  try {
    const stored = typeof window !== 'undefined' ? window.localStorage.getItem(FONT_SIZE_STORAGE_KEY) : null;
    return stored === 'large' ? 'large' : 'default';
  } catch {
    return 'default';
  }
}

export function ThemeProvider({ children }: { children: ReactNode }) {
  const [theme, setThemeState] = useState<Theme>(getInitialTheme);
  const [density, setDensityState] = useState<Density>(readDensity);
  const [fontSize, setFontSizeState] = useState<FontSize>(readFontSize);

  // Persist the user's exact choice (including 'system') and apply the
  // resolved theme to the document.
  useEffect(() => {
    document.documentElement.dataset.theme = resolveTheme(theme);
    try {
      window.localStorage.setItem(THEME_STORAGE_KEY, theme);
    } catch {
      // A blocked storage area should not stop the station from rendering.
    }
  }, [theme]);

  useEffect(() => {
    if (typeof document === 'undefined') return;
    document.documentElement.dataset.density = density;
    try {
      window.localStorage.setItem(DENSITY_STORAGE_KEY, density);
    } catch {
      // Preference persistence is best effort.
    }
  }, [density]);

  useEffect(() => {
    if (typeof document === 'undefined') return;
    document.documentElement.dataset.fontSize = fontSize;
    try {
      window.localStorage.setItem(FONT_SIZE_STORAGE_KEY, fontSize);
    } catch {
      // Preference persistence is best effort.
    }
  }, [fontSize]);

  // While following the system preference, react to OS theme changes without
  // requiring a reload.
  useEffect(() => {
    if (theme !== 'system') return;
    const mql = window.matchMedia(DARK_QUERY);
    const onChange = () => {
      document.documentElement.dataset.theme = resolveTheme('system');
    };
    mql.addEventListener('change', onChange);
    return () => mql.removeEventListener('change', onChange);
  }, [theme]);

  const setTheme = useCallback((next: Theme) => setThemeState(next), []);
  // Kept for callers that still want a binary flip (defaults light<->dark).
  const toggleTheme = useCallback(() => {
    setThemeState((current) => (current === 'light' ? 'dark' : 'light'));
  }, []);
  const setDensity = useCallback((next: Density) => setDensityState(next), []);
  const setFontSize = useCallback((next: FontSize) => setFontSizeState(next), []);

  const value = useMemo(
    () => ({ theme, setTheme, toggleTheme, density, setDensity, fontSize, setFontSize }),
    [density, fontSize, setDensity, setFontSize, setTheme, theme, toggleTheme],
  );

  return <ThemeContext.Provider value={value}>{children}</ThemeContext.Provider>;
}
