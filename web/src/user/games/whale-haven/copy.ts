import { useTranslation } from 'react-i18next';

export function useWhaleText() {
  const { i18n } = useTranslation();
  const english = (i18n.resolvedLanguage ?? i18n.language).startsWith('en');
  return (zh: string, en: string) => (english ? en : zh);
}
