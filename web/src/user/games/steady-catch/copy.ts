import { useTranslation } from 'react-i18next';
export const CLOUD_LINES = {
  combo: ['稳得像温度 0', 'Steady as temperature zero'],
  gold: ['这句我收藏了', 'Saving that one'],
  hit: ['我就说别接它…', 'I told you not to catch that…'],
  patience: ['深呼吸，还有机会', "Breathe, there's still time"],
  finale: ['要来了，极其极其', 'Here it comes, extremely'],
} as const;
export function useCatchText() {
  const { i18n } = useTranslation();
  return (zh: string, en: string) => (i18n.resolvedLanguage?.startsWith('zh') ? zh : en);
}
