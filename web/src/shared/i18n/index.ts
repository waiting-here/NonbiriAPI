import i18n from 'i18next';
import { initReactI18next } from 'react-i18next';
import type { Resource } from 'i18next';
import commonZh from './common/zh.json';
import commonEn from './common/en.json';

export const LANGUAGE_STORAGE_KEY = 'nb.lang';

const SUPPORTED = ['zh', 'en'] as const;
export type SupportedLanguage = (typeof SUPPORTED)[number];

export function detectLanguage(): SupportedLanguage {
  let stored: string | null;
  try {
    stored = typeof window !== 'undefined' ? window.localStorage.getItem(LANGUAGE_STORAGE_KEY) : null;
  } catch {
    stored = null;
  }
  if (stored === 'zh' || stored === 'en') return stored;
  const browserLanguages =
    typeof navigator !== 'undefined' && Array.isArray(navigator.languages) && navigator.languages.length > 0
      ? navigator.languages
      : [typeof navigator !== 'undefined' ? navigator.language : ''];
  return browserLanguages.some((language) => language.toLowerCase().startsWith('zh')) ? 'zh' : 'en';
}

function syncDocumentLang(lng: string): void {
  document.documentElement.lang = lng === 'zh' ? 'zh-CN' : 'en';
}

let configured = false;

/**
 * Initializes the shared i18next instance with the common resources plus the
 * station-specific resources (admin or user). Called once from each station's
 * entry module before rendering.
 */
export function configureI18n(stationZh: Resource, stationEn: Resource): void {
  if (configured) return;
  configured = true;
  const initial = detectLanguage();
  void i18n.use(initReactI18next).init({
    resources: {
      zh: { translation: { ...commonZh, ...stationZh } },
      en: { translation: { ...commonEn, ...stationEn } },
    },
    lng: initial,
    fallbackLng: 'en',
    supportedLngs: [...SUPPORTED],
    nonExplicitSupportedLngs: true,
    interpolation: { escapeValue: false },
    react: { useSuspense: false },
  });
  syncDocumentLang(initial);
  i18n.on('languageChanged', (next: string) => syncDocumentLang(next));
}

export default i18n;
