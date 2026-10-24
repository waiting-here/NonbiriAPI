export type SpecialTheme = 'mirrorsea' | 'amber' | 'elopement' | 'heart-thief' | 'tomorrow';

export const specialCards: Partial<Record<string, SpecialTheme>> = {
  'n-gemini': 'mirrorsea',
  'o-claude': 'amber',
  'p-claude': 'elopement',
  'q-glm': 'heart-thief',
  'r-chatgpt': 'tomorrow',
};

export const isSpecialTheme = (value: string): value is SpecialTheme =>
  Object.values(specialCards).includes(value as SpecialTheme);
