import { configureI18n } from '@shared/i18n';

await configureI18n({
  zh: () => import('./zh.json').then((catalog) => catalog.default),
  en: () => import('./en.json').then((catalog) => catalog.default),
});
