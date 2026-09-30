import { useTranslation } from 'react-i18next';
import type common from './common/en.json';
import type user from '../../user/i18n/en.json';
import type admin from '../../admin/i18n/en.json';

type Leaves<T> = {
  [K in keyof T & string]: T[K] extends string ? K : `${K}.${Leaves<T[K]>}`;
}[keyof T & string];
export type CopyKey = Leaves<typeof common> | Leaves<typeof user> | Leaves<typeof admin>;
export type CopyParameters = Readonly<Record<string, string | number>>;

/** Closed local aliases adapt to the root JSON authority without a second word list. */
export function useRegisteredCopy<const T extends Readonly<Record<string, CopyKey>>>(keys: T) {
  const { t, i18n } = useTranslation();
  return {
    language: i18n.resolvedLanguage?.startsWith('zh') ? ('zh' as const) : ('en' as const),
    t: (key: keyof T, parameters?: CopyParameters): string => t(keys[key], parameters),
  };
}
