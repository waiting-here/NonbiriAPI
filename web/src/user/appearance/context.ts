import { createContext, useContext } from 'react';
import catalog from '../../../../internal/appearance/catalog.json';

export const appearances = catalog;
export interface AppearanceState {
  selected: string;
  unlocked: string[];
  visible?: string[];
  updated_at: number | null;
}
export interface AppearancePort {
  scope: string;
  load(): Promise<AppearanceState>;
  select(id: string): Promise<AppearanceState>;
}
export const appearanceKey = (scope: string) => ['user', 'appearance', scope] as const;
export const AppearanceContext = createContext<{
  state: AppearanceState;
  pending: boolean;
  error: unknown;
  saving: boolean;
  select(id: string): Promise<void>;
  refresh(): void;
} | null>(null);
export const useOptionalAppearance = () => useContext(AppearanceContext);
