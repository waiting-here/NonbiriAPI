import type { CharityPriceConfig, TokenPrices } from '@shared/operations/charity';
import { createTimeDraft, timeDraftValue, type TimeDraft } from '@shared/time';

export interface PriceDraft {
  mode: 'per_request' | 'per_token';
  requestUser: string;
  requestDonor: string;
  userPrices: TokenPrices;
  donorRewards: TokenPrices;
  tokenReserveCredits: string | null;
  longEnabled: boolean;
  longThreshold: string;
  longRequest: string;
  longPrices: TokenPrices;
  penaltyEnabled: boolean;
  penaltyThreshold: string;
  penaltyFine: string;
  discountEnabled: boolean;
  discountPercent: number;
  discountStart: TimeDraft;
  discountEnd: TimeDraft;
}

export const emptyPrices = (): TokenPrices => ({
  uncached_input: '0',
  cache_write_input: '0',
  cache_read_input: '0',
  output: '0',
});

export function priceDraft(config?: CharityPriceConfig): PriceDraft {
  const pricing = config?.pricing;
  return {
    mode: pricing?.mode ?? 'per_request',
    requestUser: pricing?.mode === 'per_request' ? pricing.user_price : '0',
    requestDonor: pricing?.mode === 'per_request' ? pricing.donor_reward : '0',
    userPrices: pricing?.mode === 'per_token' ? { ...pricing.user_prices } : emptyPrices(),
    donorRewards: pricing?.mode === 'per_token' ? { ...pricing.donor_rewards } : emptyPrices(),
    tokenReserveCredits: config?.token_reserve_credits ?? null,
    longEnabled: Boolean(pricing?.long_context),
    longThreshold: pricing?.long_context?.input_threshold ?? '',
    longRequest: pricing?.mode === 'per_request' ? (pricing.long_context?.user_price ?? '0') : '0',
    longPrices:
      pricing?.mode === 'per_token' && pricing.long_context?.user_prices
        ? { ...pricing.long_context.user_prices }
        : emptyPrices(),
    penaltyEnabled: Boolean(pricing?.context_penalty),
    penaltyThreshold: pricing?.context_penalty?.input_threshold ?? '',
    penaltyFine: pricing?.context_penalty?.fine ?? '0',
    discountEnabled: config?.discount.enabled ?? false,
    discountPercent: config?.discount.percent ?? 0,
    discountStart: createTimeDraft(config?.discount.start_at ?? null, 'second'),
    discountEnd: createTimeDraft(config?.discount.end_at ?? null, 'second'),
  };
}

export function draftPriceConfig(draft: PriceDraft): CharityPriceConfig | null {
  const start = timeDraftValue(draft.discountStart);
  const end = timeDraftValue(draft.discountEnd);
  if (start === undefined || end === undefined) return null;
  return {
    pricing: {
      ...(draft.mode === 'per_request'
        ? {
            mode: 'per_request' as const,
            user_price: draft.requestUser,
            donor_reward: draft.requestDonor,
          }
        : {
            mode: 'per_token' as const,
            user_prices: { ...draft.userPrices },
            donor_rewards: { ...draft.donorRewards },
          }),
      ...(draft.longEnabled
        ? {
            long_context: {
              input_threshold: draft.longThreshold,
              ...(draft.mode === 'per_request'
                ? { user_price: draft.longRequest }
                : { user_prices: { ...draft.longPrices } }),
            },
          }
        : {}),
      ...(draft.penaltyEnabled
        ? { context_penalty: { input_threshold: draft.penaltyThreshold, fine: draft.penaltyFine } }
        : {}),
    },
    token_reserve_credits: draft.tokenReserveCredits,
    discount: {
      enabled: draft.discountEnabled,
      percent: draft.discountPercent,
      start_at: start,
      end_at: end,
    },
  };
}

export function switchPriceMode<T extends PriceDraft>(draft: T, mode: PriceDraft['mode']): T {
  return {
    ...draft,
    mode,
    requestUser: '0',
    requestDonor: '0',
    userPrices: emptyPrices(),
    donorRewards: emptyPrices(),
    longEnabled: false,
    longThreshold: '',
    longRequest: '0',
    longPrices: emptyPrices(),
    penaltyEnabled: false,
    penaltyThreshold: '',
    penaltyFine: '0',
  };
}

export function snapshotPriceDraft(draft: PriceDraft): PriceDraft {
  const keys = Object.keys(priceDraft()) as (keyof PriceDraft)[];
  return structuredClone(
    Object.fromEntries(keys.map((key) => [key, draft[key]])) as unknown as PriceDraft,
  );
}

function timeDraftSignature(draft: TimeDraft) {
  if (!draft.invalidInput && draft.text === draft.originalText) return draft.originalEpoch;
  const value = timeDraftValue(draft);
  return value === undefined
    ? { text: draft.text, zone: draft.zone, invalidInput: draft.invalidInput }
    : value;
}

/** Compare editable values without treating automatic time-zone hydration as an edit. */
export function priceDraftSignature<T extends PriceDraft>(draft: T): string {
  return JSON.stringify({
    ...draft,
    discountStart: timeDraftSignature(draft.discountStart),
    discountEnd: timeDraftSignature(draft.discountEnd),
  });
}
