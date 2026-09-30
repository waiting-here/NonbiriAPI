import type { SupportedLanguage } from './index';

export interface CatalogText {
  readonly en: string;
  readonly zh: string;
}

/** API/catalog content keeps its source identity and is never copied into a UI dictionary. */
export function catalogText(value: CatalogText, language: SupportedLanguage): string {
  return value[language];
}
