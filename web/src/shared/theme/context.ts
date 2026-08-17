import { createContext } from 'react';

export type Theme = 'light' | 'dark';

export const THEME_STORAGE_KEY = 'nb.theme';

export interface ThemeContextValue {
  theme: Theme;
  setTheme: (theme: Theme) => void;
  toggleTheme: () => void;
}

export const ThemeContext = createContext<ThemeContextValue | null>(null);
