import privacyEn from 'virtual:feature-copy/privacy/en';
import privacyZh from 'virtual:feature-copy/privacy/zh';
import termsEn from 'virtual:feature-copy/terms/en';
import termsZh from 'virtual:feature-copy/terms/zh';
import type { FeatureCatalog } from '@shared/i18n/featureCatalog';

export const privacyCopy: FeatureCatalog = {
  path: 'user.legal.privacy',
  requiredKey: 'title',
  urls: { en: privacyEn, zh: privacyZh },
};
export const termsCopy: FeatureCatalog = {
  path: 'user.legal.terms',
  requiredKey: 'title',
  urls: { en: termsEn, zh: termsZh },
};
