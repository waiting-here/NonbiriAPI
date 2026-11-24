import type { i18n as I18nInstance } from 'i18next';
import type { SupportedLanguage } from './index';

export interface FeatureCatalog {
  path: string;
  requiredKey: string;
  urls: Readonly<Record<SupportedLanguage, string>>;
}

// React exposes a wrapper per caller; the resource store is shared by every consumer of a station.
const active = new WeakMap<I18nInstance['store'], Map<FeatureCatalog, number>>();
const pending = new WeakMap<I18nInstance['store'], Map<string, Promise<void>>>();

export function observeFeature(instance: I18nInstance, catalog: FeatureCatalog) {
  const catalogs = active.get(instance.store) ?? new Map<FeatureCatalog, number>();
  active.set(instance.store, catalogs);
  catalogs.set(catalog, (catalogs.get(catalog) ?? 0) + 1);
  return () => {
    const count = catalogs.get(catalog) ?? 0;
    if (count <= 1) catalogs.delete(catalog);
    else catalogs.set(catalog, count - 1);
  };
}

export function hasFeature(
  instance: I18nInstance,
  catalog: FeatureCatalog,
  language: SupportedLanguage,
) {
  return (
    typeof instance.getResource(
      language,
      'translation',
      catalog.path + '.' + catalog.requiredKey,
    ) === 'string'
  );
}

export async function loadFeature(
  instance: I18nInstance,
  catalog: FeatureCatalog,
  language: SupportedLanguage,
) {
  if (hasFeature(instance, catalog, language)) return;
  const requests = pending.get(instance.store) ?? new Map<string, Promise<void>>();
  pending.set(instance.store, requests);
  const url = catalog.urls[language];
  const existing = requests.get(url);
  if (existing) return existing;
  const request = fetch(url)
    .then(async (response) => {
      if (!response.ok) throw new Error('Language resource unavailable');
      const copy: unknown = await response.json();
      if (
        !copy ||
        typeof copy !== 'object' ||
        typeof (copy as Record<string, unknown>)[catalog.requiredKey] !== 'string'
      )
        throw new Error('Invalid language resource');
      const bundle = catalog.path
        .split('.')
        .reduceRight<Record<string, unknown>>(
          (nested, key) => ({ [key]: nested }),
          copy as Record<string, unknown>,
        );
      instance.addResourceBundle(language, 'translation', bundle, true, true);
    })
    .finally(() => requests.delete(url));
  requests.set(url, request);
  return request;
}

export async function prepareActiveFeatures(instance: I18nInstance, language: SupportedLanguage) {
  const catalogs = active.get(instance.store);
  if (catalogs)
    await Promise.all(
      [...catalogs.keys()].map((catalog) => loadFeature(instance, catalog, language)),
    );
}
