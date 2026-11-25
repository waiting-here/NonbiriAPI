import { configureI18n } from '@shared/i18n';

await configureI18n({
  zh: () => import('virtual:station-core/user/zh').then((catalog) => catalog.default),
  en: () => import('virtual:station-core/user/en').then((catalog) => catalog.default),
});
