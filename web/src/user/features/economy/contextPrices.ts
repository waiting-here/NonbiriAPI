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
function contextRows(pricing: CharityBasePricing, t: (key: string) => string): CharityPriceRow[] {
  if (pricing.mode === 'per_request')
    return [
      {
        label: t('user.charity.requestPrice'),
        userMilli: pricing.userPriceMilli,
        discountedUserMilli: pricing.discountedUserPriceMilli,
      },
    ];
  return (
    [
      ['uncachedInput', 'user.charity.uncachedInputPrice'],
      ['cacheWriteInput', 'user.charity.cacheWriteInputPrice'],
      ['cacheReadInput', 'user.charity.cacheReadInputPrice'],
      ['output', 'user.charity.outputPrice'],
    ] as const
  ).map(([key, label]) => ({
    label: t(label),
    userMilli: pricing.userPricesMilli[key],
    discountedUserMilli: pricing.discountedUserPricesMilli[key],
  }));
}
