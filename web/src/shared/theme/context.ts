import { createContext } from 'react';

export type Theme = 'light' | 'dark' | 'system';
export type Density = 'comfortable' | 'compact';
export type FontSize = 'default' | 'large';
export type AppearanceIntensity = 'vivid' | 'balanced' | 'focus';

export const THEME_STORAGE_KEY = 'nb.theme';
export const DENSITY_STORAGE_KEY = 'nb.density';
export const FONT_SIZE_STORAGE_KEY = 'nb.font-size';
export const APPEARANCE_INTENSITY_STORAGE_KEY = 'nb.appearance-intensity';

export interface ThemeContextValue {
  theme: Theme;
  setTheme: (theme: Theme) => void;
  toggleTheme: () => void;
  density: Density;
  setDensity: (density: Density) => void;
  fontSize: FontSize;
  setFontSize: (fontSize: FontSize) => void;
  /** How strongly cosmetic artwork shows through content; stored per device. */
  intensity: AppearanceIntensity;
  setIntensity: (intensity: AppearanceIntensity) => void;
}

export const ThemeContext = createContext<ThemeContextValue | null>(null);
