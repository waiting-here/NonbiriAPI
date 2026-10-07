import { useTranslation } from 'react-i18next';
export function useCatchText() {
  const { i18n } = useTranslation();
  return (zh: string, en: string) => (i18n.resolvedLanguage?.startsWith('zh') ? zh : en);
}
