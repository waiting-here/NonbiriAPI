import { createContext, useContext } from 'react';

export const CharityModelContext = createContext<string | undefined>(undefined);

export function useCharityModelScope(): string | undefined {
  return useContext(CharityModelContext);
}
