import { useTranslation } from 'react-i18next';
import type { DuelResult } from './types';

export function useDuelText() {
  const { i18n } = useTranslation();
  return (zh: string, en: string) => (i18n.resolvedLanguage?.startsWith('zh') ? zh : en);
}

export function outcomeText(
  outcome: DuelResult<unknown, unknown>['outcome'],
  t: (zh: string, en: string) => string,
) {
  return outcome === 'win'
    ? t('胜利', 'Victory')
    : outcome === 'loss'
      ? t('惜败', 'Defeat')
      : outcome === 'draw'
        ? t('平局', 'Draw')
        : t('系统取消', 'System cancelled');
}
