import { modalityNameKeys } from '@shared/operations/modalityCopy';
import type { CharityBasePricing, CharityCapabilityPricing } from './types';
import type { CharityPriceRow } from '@shared/components/CharityPriceTable';

export function contextPriceProps(pricing: CharityCapabilityPricing, t: (key: string) => string) {
  return {
    longContext: pricing.longContext && {
      inputThreshold: pricing.longContext.inputThreshold,
      rows: contextRows(pricing.longContext, t),
    },
    contextPenalty: pricing.contextPenalty,
  };
}
export function contextRows(
  pricing: CharityBasePricing,
  t: (key: string) => string,
): CharityPriceRow[] {
  if (pricing.mode !== 'per_token')
    return [
      {
        label: t(
          pricing.mode === 'per_search_unit'
            ? 'common.modalityPricing.searchCharge'
            : 'user.charity.requestPrice',
        ),
        compactLabel: t(
          pricing.mode === 'per_search_unit'
            ? 'common.modalityPricing.perSearchUnit'
            : 'user.charity.presentation.perCall',
        ),
        userMilli: pricing.userPriceMilli,
        currentUserMilli: pricing.currentUserPriceMilli,
      },
    ];
  const keys = [
    ['uncachedInput', 'user.charity.uncachedInputPrice'],
    ['cacheWriteInput', 'user.charity.cacheWriteInputPrice'],
    ['cacheReadInput', 'user.charity.cacheReadInputPrice'],
    ['output', 'user.charity.outputPrice'],
  ] as const;
  const compactKeys = {
    uncachedInput: 'user.charity.presentation.input',
    cacheWriteInput: 'user.charity.presentation.cacheWrite',
    cacheReadInput: 'user.charity.presentation.cacheRead',
    output: 'user.charity.presentation.output',
  } as const;
  const p = pricing.userModalitiesMilli;
  const rows: CharityPriceRow[] = keys.map(([key, label]) => ({
    label:
      ((key === 'output' ? p?.outputEnabled : p?.inputEnabled)
        ? t('common.modalityPricing.fallback') + ' · '
        : '') + t(label),
    compactLabel:
      ((key === 'output' ? p?.outputEnabled : p?.inputEnabled)
        ? t('common.modalityPricing.fallback') + ' · '
        : '') + t(compactKeys[key]),
    userMilli: pricing.userPricesMilli[key],
    currentUserMilli: pricing.currentUserPricesMilli[key],
  }));
  if (p)
    for (const m of ['text', 'image', 'audio', 'video'] as const) {
      const current = pricing.currentUserModalitiesMilli?.[m];
      if (!p[m] || !current) continue;
      for (const [key, label] of keys)
        if (key === 'output' ? p.outputEnabled : p.inputEnabled)
          rows.push({
            label: t(modalityNameKeys[m]) + ' · ' + t(label),
            userMilli: p[m][key],
            currentUserMilli: current[key],
          });
    }
  return rows;
}
