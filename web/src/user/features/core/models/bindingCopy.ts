import { useTranslation } from 'react-i18next';
import en from './binding-copy/en.json';
import zh from './binding-copy/zh.json';

export function useBindingCopy() {
  const { i18n } = useTranslation();
  return (i18n.resolvedLanguage ?? i18n.language).startsWith('zh') ? zh : en;
}
