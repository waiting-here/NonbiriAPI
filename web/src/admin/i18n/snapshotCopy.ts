import en from 'virtual:feature-copy/management-snapshot/en';
import zh from 'virtual:feature-copy/management-snapshot/zh';
import type { FeatureCatalog } from '@shared/i18n/featureCatalog';

export const snapshotCopy: FeatureCatalog = {
  path: 'admin.managementHistory.snapshot',
  requiredKey: 'legacy',
  urls: { en, zh },
};
