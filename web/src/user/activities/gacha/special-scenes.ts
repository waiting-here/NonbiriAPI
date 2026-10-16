export type SpecialTheme = 'mirrorsea' | 'amber' | 'elopement' | 'heart-thief';

export const specialCards: Partial<Record<string, SpecialTheme>> = {
  'n-gemini': 'mirrorsea',
  'o-claude': 'amber',
  'p-claude': 'elopement',
  'q-glm': 'heart-thief',
};

export const isSpecialTheme = (value: string): value is SpecialTheme =>
  Object.values(specialCards).includes(value as SpecialTheme);
