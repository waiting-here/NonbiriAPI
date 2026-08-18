import { useCallback, useEffect, useMemo, useState, type ReactNode } from 'react';
import { THEME_STORAGE_KEY, ThemeContext, type Theme } from './context';

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
  const stored = typeof window !== 'undefined' ? window.localStorage.getItem(THEME_STORAGE_KEY) : null;
  const theme: Theme =
    stored === 'light' || stored === 'dark' || stored === 'system' ? stored : 'system';
  // The embedded station shell uses a strict script CSP, so apply the initial
  // resolved theme from module code before the first React paint instead of
  // relying on an inline HTML bootstrap script.
  document.documentElement.dataset.theme = resolveTheme(theme);
  return theme;
}

export function ThemeProvider({ children }: { children: ReactNode }) {
  const [theme, setThemeState] = useState<Theme>(getInitialTheme);

  // Persist the user's exact choice (including 'system') and apply the
  // resolved theme to the document.
  useEffect(() => {
    document.documentElement.dataset.theme = resolveTheme(theme);
    window.localStorage.setItem(THEME_STORAGE_KEY, theme);
  }, [theme]);

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

  const value = useMemo(() => ({ theme, setTheme, toggleTheme }), [theme, setTheme, toggleTheme]);

  return <ThemeContext.Provider value={value}>{children}</ThemeContext.Provider>;
}
