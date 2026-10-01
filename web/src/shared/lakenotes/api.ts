import { decoded, idempotentOptions, queryPath } from '@shared/operations/api';
import { ApiError, apiFetch, type ApiRequestOptions } from '@shared/query/http';

export const directions = [
  'coins_to_general',
  'general_to_coins',
  'coins_to_game',
  'game_to_coins',
] as const;
export type Direction = (typeof directions)[number];
export type Unit = 'coins' | 'general' | 'game';
export const directionUnits: Record<Direction, readonly [Unit, Unit]> = {
  coins_to_general: ['coins', 'general'],
  general_to_coins: ['general', 'coins'],
  coins_to_game: ['coins', 'game'],
  game_to_coins: ['game', 'coins'],
};
export const base = '/api/limited-activities/lake-notes';
export const adminBase = '/admin/api/limited-activities/lake-notes';
export interface ExchangeSetting {
  enabled: boolean;
  source_amount: string;
  target_amount: string;
}
export interface Period {
  id: string;
  revision: string;
  name: string;
  status: 'draft' | 'published' | 'cancelled';
  starts_at: number;
  ends_at: number;
  entry_fee_milli: string | null;
  exchanges: Record<Direction, ExchangeSetting>;
}
export type PeriodInput = Omit<Period, 'id' | 'revision'> & { expected_revision: string };
export function decodePeriod(value: unknown, publicProjection = false): Period {
  const period = value as Period;
  return {
    ...period,
    status: publicProjection ? 'published' : period.status,
    exchanges: Object.fromEntries(
      directions.map((direction) => [
        direction,
        period.exchanges[direction] ?? { enabled: false, source_amount: '', target_amount: '' },
      ]),
    ) as Period['exchanges'],
  };
}
export interface LakeDirectory {
  key: 'lake-notes';
  name: string;
  cover_key: 'lake-notes';
  visible: boolean;
  paused: boolean;
  starts_at: number | null;
  ends_at: number | null;
  revision: string;
  status: 'unconfigured' | 'unavailable' | 'scheduled' | 'open' | 'paused' | 'ended';
  module_config: { periods: Period[] };
}
export function decodeLakeDetail(value: unknown): LakeDirectory {
  const detail = value as LakeDirectory;
  return {
    ...detail,
    module_config: {
      periods: (detail.module_config.periods ?? []).map((period) => decodePeriod(period, true)),
    },
  };
}
export const getLakeDetail = (options?: ApiRequestOptions) =>
  decoded(base, decodeLakeDetail, options);
export const getLakeConfig = (options?: ApiRequestOptions) =>
  decoded(adminBase, decodeLakeDetail, options);
export interface LakeConfigInput {
  expected_revision: string;
  visible: boolean;
  paused: boolean;
  starts_at: number | null;
  ends_at: number | null;
  module_config: Record<string, never>;
}
export const updateLakeConfig = (
  input: LakeConfigInput,
  key: string,
  options?: ApiRequestOptions,
) =>
  decoded(
    adminBase,
    decodeLakeDetail,
    idempotentOptions(key, { ...options, method: 'PUT', json: input }),
  );
export const getPeriods = async (page = 1, options?: ApiRequestOptions) => {
  const result = await apiFetch<{
    items: Period[];
    page: number;
    page_size: number;
    has_more: boolean;
  }>(queryPath(adminBase + '/periods', { page, page_size: 20 }), options);
  return { ...result, items: result.items.map((period) => decodePeriod(period)) };
};
export const savePeriod = (
  id: string | null,
  input: PeriodInput,
  key: string,
  options?: ApiRequestOptions,
) =>
  decoded(
    adminBase + '/periods' + (id ? '/' + encodeURIComponent(id) : ''),
    decodePeriod,
    idempotentOptions(key, { ...options, method: id ? 'PUT' : 'POST', json: input }),
  );

/** Natural credits are parsed as decimal text; coins never pass through Number. */
export function naturalToUnits(text: string, unit: Unit, allowZero = false): string {
  if (!/^(0|[1-9][0-9]*)(?:\.[0-9]{1,3})?$/.test(text) || (unit === 'coins' && text.includes('.')))
    throw new ApiError(
      'invalid_request',
      'Enter a whole coin amount or credits with at most three decimals.',
      400,
    );
  const [whole, fraction = ''] = text.split('.');
  const value =
    unit === 'coins' ? BigInt(whole) : BigInt(whole) * 1000n + BigInt(fraction.padEnd(3, '0'));
  if ((!allowZero && value === 0n) || value > (1n << 128n) - 1n)
    throw new ApiError('invalid_request', 'Amount is outside the supported range.', 400);
  return String(value);
}
export function unitsToNatural(text: string, unit: Unit): string {
  if (unit === 'coins') return text;
  const signed = BigInt(text),
    v = signed < 0n ? -signed : signed;
  const f = (v % 1000n).toString().padStart(3, '0').replace(/0+$/, '');
  return (signed < 0n ? '-' : '') + String(v / 1000n) + (f ? '.' + f : '');
}
