import { useTranslation } from 'react-i18next';
import en from './matching-copy/en.json';
import zh from './matching-copy/zh.json';

export function useMainstreamMatchingCopy() {
  const { i18n } = useTranslation();
  return (i18n.resolvedLanguage ?? i18n.language).startsWith('zh') ? zh : en;
}
