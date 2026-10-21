import {
  defaultRecurringDiscount,
  validRecurringDiscount,
  type RecurringDiscount,
} from '@shared/operations/recurringDiscount';
import type { CharityPriceConfig, ModalityPricing, TokenPrices } from '@shared/operations/charity';
import { createTimeDraft, timeDraftValue, type TimeDraft } from '@shared/time';
import { MAX_UNIX_SECOND, validAmount, validTokenReserveCredits } from './managementFields';

export interface PriceDraft {
  mode: 'per_request' | 'per_token' | 'per_search_unit';
  userModalities: ModalityPricing;
  donorModalities: ModalityPricing;
  longDonorOverride: boolean;
  longDonorRequest: string;
  longDonorRewards: TokenPrices;
  longUserModalities: ModalityPricing;
  longDonorModalities: ModalityPricing | null;
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
  recurringDiscount: RecurringDiscount;
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

export const emptyModalities = (): ModalityPricing => ({
  input_enabled: false,
  output_enabled: false,
});
export function priceDraft(config?: CharityPriceConfig): PriceDraft {
  const pricing = config?.pricing;
  return {
    mode: pricing?.mode ?? 'per_request',
    userModalities: structuredClone(pricing?.user_modalities ?? emptyModalities()),
    donorModalities: structuredClone(pricing?.donor_modalities ?? emptyModalities()),
    longUserModalities: structuredClone(
      pricing?.long_context?.user_modalities ?? emptyModalities(),
    ),
    longDonorModalities: pricing?.long_context?.donor_modalities
      ? structuredClone(pricing.long_context.donor_modalities)
      : null,
    longDonorOverride:
      pricing?.long_context?.donor_reward !== undefined ||
      pricing?.long_context?.donor_rewards !== undefined,
    longDonorRequest: pricing?.long_context?.donor_reward ?? '0',
    longDonorRewards: structuredClone(pricing?.long_context?.donor_rewards ?? emptyPrices()),
    requestUser: pricing && pricing.mode !== 'per_token' ? pricing.user_price : '0',
    requestDonor: pricing && pricing.mode !== 'per_token' ? pricing.donor_reward : '0',
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
    recurringDiscount: structuredClone(config?.recurring_discount ?? defaultRecurringDiscount()),
    discountEnabled: config?.discount.enabled ?? false,
    discountPercent: config?.discount.percent ?? 0,
    discountStart: createTimeDraft(config?.discount.start_at ?? null, 'second'),
    discountEnd: createTimeDraft(config?.discount.end_at ?? null, 'second'),
  };
}

function hasModalities(value: ModalityPricing) {
  return (
    value.input_enabled ||
    value.output_enabled ||
    ['text', 'image', 'audio', 'video'].some((key) => Object.hasOwn(value, key))
  );
}
export function draftPriceConfig(draft: PriceDraft): CharityPriceConfig | null {
  const start = timeDraftValue(draft.discountStart);
  const end = timeDraftValue(draft.discountEnd);
  if (start === undefined || end === undefined) return null;
  return {
    pricing: {
      ...(draft.mode !== 'per_token'
        ? {
            mode: draft.mode,
            user_price: draft.requestUser,
            donor_reward: draft.requestDonor,
          }
        : {
            mode: 'per_token' as const,
            user_prices: { ...draft.userPrices },
            donor_rewards: { ...draft.donorRewards },
          }),
      ...(draft.mode === 'per_token'
        ? {
            ...(hasModalities(draft.userModalities)
              ? { user_modalities: structuredClone(draft.userModalities) }
              : {}),
            ...(hasModalities(draft.donorModalities)
              ? { donor_modalities: structuredClone(draft.donorModalities) }
              : {}),
          }
        : {}),
      ...(draft.longEnabled && draft.mode !== 'per_search_unit'
        ? {
            long_context: {
              input_threshold: draft.longThreshold,
              ...(draft.mode === 'per_token'
                ? {
                    ...(hasModalities(draft.longUserModalities)
                      ? { user_modalities: structuredClone(draft.longUserModalities) }
                      : {}),
                    ...(draft.longDonorModalities
                      ? { donor_modalities: structuredClone(draft.longDonorModalities) }
                      : {}),
                  }
                : {}),
              ...(draft.longDonorOverride
                ? draft.mode === 'per_request'
                  ? { donor_reward: draft.longDonorRequest }
                  : { donor_rewards: structuredClone(draft.longDonorRewards) }
                : {}),
              ...(draft.mode !== 'per_token'
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
    recurring_discount: structuredClone(draft.recurringDiscount),
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
    userModalities: emptyModalities(),
    donorModalities: emptyModalities(),
    longUserModalities: emptyModalities(),
    longDonorModalities: null,
    longDonorOverride: false,
    longDonorRequest: '0',
    longDonorRewards: emptyPrices(),
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

export function priceDraftError(
  draft: PriceDraft,
):
  | 'contextThreshold'
  | 'modelPrices'
  | 'tokenReserveCredits'
  | 'discountPercent'
  | 'discountDates'
  | 'recurringDiscount'
  | null {
  const prices =
    draft.mode !== 'per_token'
      ? [draft.requestUser, draft.requestDonor]
      : [...Object.values(draft.userPrices), ...Object.values(draft.donorRewards)];
  if (draft.longEnabled)
    prices.push(
      ...(draft.mode !== 'per_token' ? [draft.longRequest] : Object.values(draft.longPrices)),
    );
  if (draft.longEnabled && draft.longDonorOverride)
    prices.push(
      ...(draft.mode === 'per_request'
        ? [draft.longDonorRequest]
        : Object.values(draft.longDonorRewards)),
    );
  if (draft.mode === 'per_token')
    for (const p of [
      draft.userModalities,
      draft.donorModalities,
      ...(draft.longEnabled ? [draft.longUserModalities, draft.longDonorModalities] : []),
    ]) {
      for (const key of ['text', 'image', 'audio', 'video'] as const)
        if (p?.[key]) prices.push(...Object.values(p[key]));
    }
  if (draft.penaltyEnabled) prices.push(draft.penaltyFine);
  if (
    (draft.longEnabled && !/^[1-9][0-9]*$/.test(draft.longThreshold)) ||
    (draft.penaltyEnabled && !/^[1-9][0-9]*$/.test(draft.penaltyThreshold))
  )
    return 'contextThreshold';
  if (prices.some((value) => !validAmount(value))) return 'modelPrices';
  if (!validTokenReserveCredits(draft.tokenReserveCredits)) return 'tokenReserveCredits';
  if (
    !Number.isInteger(draft.discountPercent) ||
    draft.discountPercent < 0 ||
    draft.discountPercent > 100
  )
    return 'discountPercent';
  const start = timeDraftValue(draft.discountStart);
  const end = timeDraftValue(draft.discountEnd);
  if (
    start === undefined ||
    end === undefined ||
    (start !== null && (!Number.isSafeInteger(start) || start < 0 || start > MAX_UNIX_SECOND)) ||
    (end !== null && (!Number.isSafeInteger(end) || end < 0 || end > MAX_UNIX_SECOND)) ||
    (start !== null && end !== null && end < start)
  )
    return 'discountDates';
  if (!validRecurringDiscount(draft.recurringDiscount)) return 'recurringDiscount';
  return null;
}
