import i18n from 'i18next';
import { prepareActiveFeatures } from './featureCatalog';
import { initReactI18next } from 'react-i18next';
import type { BackendModule, i18n as I18nInstance, ResourceLanguage } from 'i18next';

export const LANGUAGE_STORAGE_KEY = 'nb.lang';

const SUPPORTED = ['zh', 'en'] as const;
export type SupportedLanguage = (typeof SUPPORTED)[number];

export function detectLanguage(): SupportedLanguage {
  let stored: string | null;
  try {
    stored =
      typeof window !== 'undefined' ? window.localStorage.getItem(LANGUAGE_STORAGE_KEY) : null;
  } catch {
    stored = null;
  }
  if (stored === 'zh' || stored === 'en') return stored;
  const browserLanguages =
    typeof navigator !== 'undefined' &&
    Array.isArray(navigator.languages) &&
    navigator.languages.length > 0
      ? navigator.languages
      : [typeof navigator !== 'undefined' ? navigator.language : ''];
  return browserLanguages.some((language) => language.toLowerCase().startsWith('zh')) ? 'zh' : 'en';
}

function syncDocumentLang(lng: string): void {
  document.documentElement.lang = lng === 'zh' ? 'zh-CN' : 'en';
}

type CatalogLoaders = Record<SupportedLanguage, () => Promise<ResourceLanguage>>;

const common: CatalogLoaders = {
  zh: () => import('./common/zh.json').then((catalog) => catalog.default),
  en: () => import('./common/en.json').then((catalog) => catalog.default),
};
let loadCatalog: ((language: SupportedLanguage) => Promise<ResourceLanguage>) | undefined;

// Forms that persist a language choice must know that its UI can actually render first.
export async function prepareLanguage(instance: I18nInstance, language: SupportedLanguage) {
  if (
    instance.store === i18n.store &&
    loadCatalog &&
    !instance.hasResourceBundle(language, 'translation')
  ) {
    instance.addResourceBundle(language, 'translation', await loadCatalog(language));
  }
  await prepareActiveFeatures(instance, language);
}

export async function configureI18n(station: CatalogLoaders): Promise<void> {
  const initial = detectLanguage();
  syncDocumentLang(initial);
  const root = document.getElementById('root');
  const message = document.createElement('p');
  if (root && !root.hasChildNodes()) {
    message.className = 'station-startup';
    message.setAttribute('role', 'status');
    message.textContent = initial === 'zh' ? '正在载入页面…' : 'Loading page…';
    root.append(message);
  }
  const pending = new Map<SupportedLanguage, Promise<ResourceLanguage>>();
  const load = (language: SupportedLanguage) => {
    const existing = pending.get(language);
    if (existing) return existing;
    const resource = Promise.all([common[language](), station[language]()])
      .then(([shared, local]) => ({ ...shared, ...local }))
      .finally(() => pending.delete(language));
    pending.set(language, resource);
    return resource;
  };
  loadCatalog = load;
  const backend: BackendModule = {
    type: 'backend',
    init() {},
    read(language, _namespace, done) {
      if (language !== 'zh' && language !== 'en') {
        done(new Error('Unsupported language'), false);
        return;
      }
      void load(language).then(
        (data) => done(null, data),
        (error) => done(error, false),
      );
    },
  };
  try {
    const resource = await load(initial);
    await i18n
      .use(backend)
      .use(initReactI18next)
      .init({
        resources: { [initial]: { translation: resource } },
        partialBundledLanguages: true,
        lng: initial,
        supportedLngs: [...SUPPORTED],
        load: 'languageOnly',
        // Both locales must have identical keys before a build can ship.
        fallbackLng: false,
        interpolation: { escapeValue: false },
        react: { useSuspense: false },
      });
    i18n.off('languageChanged', syncDocumentLang);
    i18n.on('languageChanged', syncDocumentLang);
  } catch (error) {
    if (message.isConnected) {
      message.setAttribute('role', 'alert');
      message.textContent =
        initial === 'zh' ? '页面资源加载失败。' : 'Page resources could not be loaded.';
      const retry = document.createElement('button');
      retry.type = 'button';
      retry.className = 'nb-btn nb-btn--secondary';
      retry.textContent = initial === 'zh' ? '重新加载' : 'Reload';
      retry.onclick = () => window.location.reload();
      message.append(' ', retry);
    }
    throw error;
  }
}

export default i18n;
