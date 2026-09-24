import { useTranslation } from 'react-i18next';
import type { ActivityStatus, ActivityAsset } from './api';
export function useActivityText() {
  const { i18n } = useTranslation();
  return (zh: string, en: string) => (i18n.resolvedLanguage?.startsWith('zh') ? zh : en);
}
export type ActivityText = ReturnType<typeof useActivityText>;
export function statusLabel(status: ActivityStatus, t: ActivityText) {
  return {
    unconfigured: t('活动未开启', 'Not configured'),
    unavailable: t('活动暂不可用', 'Temporarily unavailable'),
    scheduled: t('活动尚未开始', 'Not started'),
    open: t('活动进行中', 'Open'),
    paused: t('活动已暂停', 'Paused'),
    ended: t('活动已结束', 'Ended'),
  }[status];
}
export function currencyLabel(asset: ActivityAsset, t: ActivityText) {
  return asset === 'sketch_paper' ? t('草稿纸', 'Sketch paper') : t('画笔', 'Paint brushes');
}
