import type { ReactNode } from 'react';
import { CharityModelContext } from './charityModelScopeContext';

export function CharityModelScope({
  modelID,
  children,
}: {
  modelID?: string;
  children: ReactNode;
}) {
  return <CharityModelContext.Provider value={modelID}>{children}</CharityModelContext.Provider>;
}
