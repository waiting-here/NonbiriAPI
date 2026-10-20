import { describe, expect, it } from 'vitest';
import type { CharityPriceConfig } from '@shared/operations/charity';
import {
  draftPriceConfig,
  priceDraft,
  priceDraftSignature,
  snapshotPriceDraft,
  switchPriceMode,
} from './priceDraft';
import {
  editTimeDraft,
  rezoneSiteTimeDraft,
  resolveFixedLocalTime,
  timeDraftKey,
} from '@shared/time';

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
  recurring_discount: {
    enabled: true,
    time_zone: 'America/New_York',
    default_percent: 50,
    periods: [{ weekdays: [1, 7], start_time: '22:00', end_time: '02:00', percent: 0 }],
  },
};

describe('complete charity price snapshots', () => {
  it('round trips all token prices, rewards, reserve, context settings, and discount', () => {
    expect(draftPriceConfig(priceDraft(token))).toEqual(token);
    const draft = priceDraft(token);
    draft.userPrices.output = '99';
    draft.longPrices.output = '199';
    draft.recurringDiscount.periods[0]!.weekdays.push(2);
    expect(token.recurring_discount.periods[0]!.weekdays).toEqual([1, 7]);
    const restored = snapshotPriceDraft(priceDraft(token));
    const body = draftPriceConfig(restored)!;
    body.recurring_discount.periods[0]!.percent = 99;
    expect(restored.recurringDiscount.periods[0]!.percent).toBe(0);
    expect(token.pricing.mode === 'per_token' && token.pricing.user_prices.output).toBe('60');
    expect(token.pricing.long_context?.user_prices?.output).toBe('120');
  });

  it('fills a request snapshot including false, zero, null, and removes all token-only and disabled drafts', () => {
    const request: CharityPriceConfig = {
      pricing: { mode: 'per_request', user_price: '3.5', donor_reward: '1.75' },
      token_reserve_credits: null,
      discount: { enabled: false, percent: 0, start_at: null, end_at: null },
      recurring_discount: { enabled: false, time_zone: 'UTC', default_percent: 0, periods: [] },
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

describe('charity draft leave protection', () => {
  it.each([null, 480, -240])('ignores automatic site-time hydration at offset %s', (offset) => {
    for (const draft of [priceDraft(), priceDraft(token)]) {
      expect(
        priceDraftSignature({
          ...draft,
          discountStart: rezoneSiteTimeDraft(draft.discountStart, offset),
          discountEnd: rezoneSiteTimeDraft(draft.discountEnd, offset),
        }),
      ).toBe(priceDraftSignature(draft));
    }
  });

  it('keeps unresolved, invalid and cleared date changes dirty', () => {
    const draft = priceDraft(token);
    const saved = priceDraftSignature(draft);
    const start = rezoneSiteTimeDraft(draft.discountStart, 480);
    for (const discountStart of [
      editTimeDraft(start, '2026-10-20T09:00:00'),
      editTimeDraft(start, '', true),
      editTimeDraft(start, ''),
    ]) {
      expect(priceDraftSignature({ ...draft, discountStart })).not.toBe(saved);
    }
  });

  it('compares resolved dates by instant while retaining price and model edits', () => {
    const draft = { ...priceDraft(token), provider: 'Example' };
    const edited = editTimeDraft(
      rezoneSiteTimeDraft(draft.discountStart, 480),
      '2026-10-20T09:00:00',
    );
    const resolved = {
      ...edited,
      resolved: { key: timeDraftKey(edited), value: resolveFixedLocalTime(edited.text, 480) },
    };
    const changed = { ...draft, discountStart: resolved };
    expect(priceDraftSignature(changed)).not.toBe(priceDraftSignature(draft));
    const saved = {
      ...priceDraft({
        ...token,
        discount: { ...token.discount, start_at: resolved.resolved.value.instant },
      }),
      provider: 'Example',
    };
    expect(priceDraftSignature(changed)).toBe(priceDraftSignature(saved));
    expect(priceDraftSignature({ ...saved, provider: 'Changed' })).not.toBe(
      priceDraftSignature(saved),
    );
    expect(priceDraftSignature({ ...saved, tokenReserveCredits: '30' })).not.toBe(
      priceDraftSignature(saved),
    );
  });
});
