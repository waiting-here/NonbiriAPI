import { createContext } from 'react';

export const FilterFieldLayoutContext = createContext(false);

export const FieldLabelContext = createContext<string | undefined>(undefined);
