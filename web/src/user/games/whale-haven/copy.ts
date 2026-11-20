import { useTranslation } from 'react-i18next';
import type { Profile } from './api';

export function useWhaleText() {
  const { i18n } = useTranslation();
  const english = (i18n.resolvedLanguage ?? i18n.language).startsWith('en');
  return (zh: string, en: string) => (english ? en : zh);
}

export function guestVisitLabel(
  guest: Pick<Profile['social']['guests'][number], 'working' | 'allow_hire' | 'hire_blocked'>,
  t: ReturnType<typeof useWhaleText>,
) {
  if (guest.working) return t('正在帮工', 'Working');
  if (guest.hire_blocked === 'quota')
    return t('今日社交鲸币已满', 'Daily social coin limit reached');
  if (guest.hire_blocked === 'wallet_full') return t('暂时无法收取工资', 'Unable to receive wages');
  return guest.allow_hire ? t('愿意帮工', 'Available to hire') : t('只来串门', 'Visiting only');
}
