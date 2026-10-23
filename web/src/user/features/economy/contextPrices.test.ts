import { expect, it } from 'vitest';
import { contextRows, contextPriceProps } from './contextPrices';
import type { CharityCapabilityPricing } from './types';
it('shows enabled classification sides, keeps free rates, and labels the aggregate fallback', () => {
  const rates = {
    uncachedInput: '1000',
    cacheWriteInput: '1000',
    cacheReadInput: '1000',
    output: '1000',
  };
  const free = { uncachedInput: '0', cacheWriteInput: '0', cacheReadInput: '0', output: '0' };
  const pricing: CharityCapabilityPricing = {
    mode: 'per_token',
    userPricesMilli: rates,
    currentUserPricesMilli: rates,
    discountedUserPricesMilli: rates,
    userModalitiesMilli: { inputEnabled: true, outputEnabled: false, audio: free },
    currentUserModalitiesMilli: { inputEnabled: true, outputEnabled: false, audio: free },
  };
  const rows = contextRows(pricing, (key) => key);
  expect(rows).toHaveLength(7);
  expect(rows[0].label).toContain('fallback');
  expect(rows[3].label).not.toContain('fallback');
  expect(rows.slice(4).every((row) => row.currentUserMilli === '0')).toBe(true);
  const tier = { ...pricing, inputThreshold: '100' };
  expect(
    contextPriceProps({ ...pricing, longContext: tier }, (key) => key).longContext?.rows,
  ).toEqual(rows);
});
it('shows search-unit prices as unit prices', () => {
  const rows = contextRows(
    {
      mode: 'per_search_unit',
      userPriceMilli: '1',
      discountedUserPriceMilli: '1',
      currentUserPriceMilli: '1',
    },
    (key) => key,
  );
  expect(rows).toEqual([
    {
      label: 'common.modalityPricing.searchCharge',
      compactLabel: 'common.modalityPricing.perSearchUnit',
      userMilli: '1',
      currentUserMilli: '1',
    },
  ]);
});
