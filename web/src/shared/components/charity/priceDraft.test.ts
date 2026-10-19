import { describe, expect, it } from 'vitest';
import type { CharityPriceConfig } from '@shared/operations/charity';
import { draftPriceConfig, priceDraft, snapshotPriceDraft, switchPriceMode } from './priceDraft';

const token: CharityPriceConfig = {
  pricing: {
    mode: 'per_token',
    user_prices: {
      uncached_input: '20',
      cache_write_input: '24',
      cache_read_input: '2',
      output: '60',
    },
    donor_rewards: {
      uncached_input: '10',
      cache_write_input: '12',
      cache_read_input: '1',
      output: '30',
    },
    long_context: {
      input_threshold: '100000',
      user_prices: {
        uncached_input: '40',
        cache_write_input: '48',
        cache_read_input: '4',
        output: '120',
      },
    },
    context_penalty: { input_threshold: '200000', fine: '500' },
  },
  token_reserve_credits: '25.125',
  discount: { enabled: true, percent: 80, start_at: 100, end_at: 200 },
};

describe('complete charity price snapshots', () => {
  it('round trips all token prices, rewards, reserve, context settings, and discount', () => {
    expect(draftPriceConfig(priceDraft(token))).toEqual(token);
    const draft = priceDraft(token);
    draft.userPrices.output = '99';
    draft.longPrices.output = '199';
    expect(token.pricing.mode === 'per_token' && token.pricing.user_prices.output).toBe('60');
    expect(token.pricing.long_context?.user_prices?.output).toBe('120');
  });

  it('fills a request snapshot including false, zero, null, and removes all token-only and disabled drafts', () => {
    const request: CharityPriceConfig = {
      pricing: { mode: 'per_request', user_price: '3.5', donor_reward: '1.75' },
      token_reserve_credits: null,
      discount: { enabled: false, percent: 0, start_at: null, end_at: null },
    };
    const filled = { ...priceDraft(token), ...priceDraft(request) };
    expect(draftPriceConfig(filled)).toEqual(request);
    expect(filled).toMatchObject({
      longEnabled: false,
      longThreshold: '',
      longRequest: '0',
      penaltyEnabled: false,
      penaltyThreshold: '',
      penaltyFine: '0',
      userPrices: {
        uncached_input: '0',
        cache_write_input: '0',
        cache_read_input: '0',
        output: '0',
      },
      donorRewards: {
        uncached_input: '0',
        cache_write_input: '0',
        cache_read_input: '0',
        output: '0',
      },
      longPrices: {
        uncached_input: '0',
        cache_write_input: '0',
        cache_read_input: '0',
        output: '0',
      },
    });
    const reverted = snapshotPriceDraft(priceDraft(token));
    expect(draftPriceConfig(reverted)).toEqual(token);
  });

  it('manual mode switches cannot revive hidden token or context prices', () => {
    const request = switchPriceMode(priceDraft(token), 'per_request');
    const back = switchPriceMode(request, 'per_token');
    expect(back.userPrices.output).toBe('0');
    expect(back.longEnabled).toBe(false);
    expect(back.longThreshold).toBe('');
    expect(back.penaltyFine).toBe('0');
    expect(back.tokenReserveCredits).toBe('25.125');
  });
});
