import { decoded, idempotentOptions } from '@shared/operations/api';
import {
  amount,
  array,
  boolean,
  decimal,
  integer,
  invalidResponse,
  nullableUnixSecond,
  oneOf,
  opaqueID,
  record,
  string,
  unixSecond,
} from '@shared/operations/wire';
import { ApiError } from '@shared/query/http';

const assets = ['sketch_paper', 'sketch_brush'] as const;
export type ActivityAsset = (typeof assets)[number];
export const statuses = [
  'unconfigured',
  'unavailable',
  'scheduled',
  'open',
  'paused',
  'ended',
] as const;
export type ActivityStatus = (typeof statuses)[number];
const maxMilli = (1n << 127n) - 1n,
  maxPrice = 9_000_000_000_000_000n;
function sequence(v: unknown) {
  const result = decimal(string(v, 'revision', { min: 1, max: 19 }), 'revision', {
    positive: true,
  });
  if (BigInt(result) > 9_223_372_036_854_775_807n) invalidResponse('revision');
  return result;
}
function units(v: unknown, positive = false) {
  const result = decimal(string(v, 'currency quantity', { min: 1, max: 39 }), 'currency quantity', {
    positive,
  });
  if (BigInt(result) * 1000n > maxMilli) invalidResponse('currency quantity');
  return result;
}
function positiveAmount(v: unknown, maximum = maxMilli) {
  const result = amount(v, 'price', false, maximum);
  if (result === '0') invalidResponse('price');
  return result;
}
export function decodeSupply(value: unknown) {
  const v = record(
    value,
    ['paper_price', 'brush_price', 'brush_cap', 'brush_exchanged', 'brush_remaining'],
    'exchange supply',
  );
  const cap = decimal(v.brush_cap, 'brush cap'),
    used = decimal(v.brush_exchanged, 'brush exchanged'),
    remaining = decimal(v.brush_remaining, 'brush remaining');
  if (BigInt(remaining) !== (BigInt(cap) > BigInt(used) ? BigInt(cap) - BigInt(used) : 0n))
    invalidResponse('exchange capacity');
  return {
    paper_price: positiveAmount(v.paper_price, maxPrice),
    brush_price: positiveAmount(v.brush_price, maxPrice),
    brush_cap: cap,
    brush_exchanged: used,
    brush_remaining: remaining,
  };
}
export type Supply = ReturnType<typeof decodeSupply>;
export function decodeDetail(value: unknown) {
  const v = record(
    value,
    [
      'key',
      'name',
      'visible',
      'starts_at',
      'ends_at',
      'paused',
      'revision',
      'status',
      'module_config',
    ],
    'activity',
  );
  const start = nullableUnixSecond(v.starts_at, 'opening time'),
    end = nullableUnixSecond(v.ends_at, 'closing time');
  if ((start === null) !== (end === null) || (start !== null && end !== null && start >= end))
    invalidResponse('activity time range');
  return {
    key: oneOf(v.key, ['picture-book'] as const, 'activity key'),
    name: string(v.name, 'activity name', { min: 1, max: 128 }),
    visible: boolean(v.visible, 'visibility'),
    starts_at: start,
    ends_at: end,
    paused: boolean(v.paused, 'pause'),
    revision: sequence(v.revision),
    status: oneOf(v.status, statuses, 'activity status'),
    module_config: decodeSupply(v.module_config),
  };
}
export type ActivityDetail = ReturnType<typeof decodeDetail>;
export function decodeWallet(value: unknown) {
  const v = record(value, ['general', 'sketch_paper', 'sketch_brush'], 'activity wallet');
  return {
    general: amount(v.general, 'general credits'),
    sketch_paper: units(v.sketch_paper),
    sketch_brush: units(v.sketch_brush),
  };
}
export type ActivityWallet = ReturnType<typeof decodeWallet>;
export function decodeExchange(value: unknown) {
  const v = record(value, ['receipt', 'wallet', 'supply'], 'exchange');
  const r = record(
    v.receipt,
    [
      'operation_id',
      'activity_key',
      'config_revision',
      'asset',
      'quantity',
      'unit_price',
      'cost',
      'ledger_seq',
      'created_at',
    ],
    'exchange receipt',
  );
  const quantity = units(r.quantity, true),
    unit = positiveAmount(r.unit_price, maxPrice),
    cost = positiveAmount(r.cost);
  if (milli(cost) !== milli(unit) * BigInt(quantity)) invalidResponse('exchange cost');
  return {
    receipt: {
      operation_id: opaqueID(r.operation_id, 'op_', 'operation'),
      activity_key: oneOf(r.activity_key, ['picture-book'] as const, 'activity key'),
      config_revision: sequence(r.config_revision),
      asset: oneOf(r.asset, assets, 'activity asset'),
      quantity,
      unit_price: unit,
      cost,
      ledger_seq: sequence(r.ledger_seq),
      created_at: unixSecond(r.created_at, 'exchange time'),
    },
    wallet: decodeWallet(v.wallet),
    supply: decodeSupply(v.supply),
  };
}
export type ExchangeResult = ReturnType<typeof decodeExchange>;
export interface ExchangeInput {
  asset: ActivityAsset;
  quantity: string;
}
export interface ActivityConfigInput {
  expected_revision: string;
  visible: boolean;
  starts_at: number | null;
  ends_at: number | null;
  paused: boolean;
  module_config: Pick<Supply, 'paper_price' | 'brush_price' | 'brush_cap'>;
}
export const getDirectory = () =>
  decoded('/api/limited-activities', (v) => array(v, 'limited activities', 64).map(decodeDetail));
export const getDetail = () => decoded('/api/limited-activities/picture-book', decodeDetail);
export const getWallet = () => decoded('/api/limited-activities/picture-book/wallet', decodeWallet);
export const getAdminConfig = () =>
  decoded('/admin/api/limited-activities/picture-book', decodeDetail);
export function exchange(input: ExchangeInput, key: string) {
  try {
    units(input.quantity, true);
    oneOf(input.asset, assets, 'activity asset');
  } catch {
    throw new ApiError('invalid_request', 'Enter a positive whole quantity.', 400);
  }
  return decoded(
    '/api/limited-activities/picture-book/exchange',
    decodeExchange,
    idempotentOptions(key, { method: 'POST', json: input }),
  );
}
export function updateConfig(input: ActivityConfigInput, key: string) {
  try {
    sequence(input.expected_revision);
    input = {
      ...input,
      module_config: {
        ...input.module_config,
        paper_price: normalizePrice(input.module_config.paper_price),
        brush_price: normalizePrice(input.module_config.brush_price),
      },
    };
    decimal(string(input.module_config.brush_cap, 'brush cap', { max: 39 }), 'brush cap');
  } catch {
    throw new ApiError('invalid_request', 'Check the prices and brush cap.', 400);
  }
  return decoded(
    '/admin/api/limited-activities/picture-book',
    decodeDetail,
    idempotentOptions(key, { method: 'PUT', json: input }),
  );
}
export function milli(text: string): bigint {
  const negative = text.startsWith('-'),
    [whole, fraction = ''] = (negative ? text.slice(1) : text).split('.');
  return (BigInt(whole) * 1000n + BigInt(fraction.padEnd(3, '0'))) * (negative ? -1n : 1n);
}
export function exchangeCost(price: string, quantity: string): string | null {
  if (!/^[1-9][0-9]{0,38}$/.test(quantity)) return null;
  const count = BigInt(quantity),
    cost = milli(price) * count;
  if (count * 1000n > maxMilli || cost > maxMilli) return null;
  const whole = cost / 1000n,
    fraction = (cost % 1000n).toString().padStart(3, '0').replace(/0+$/, '');
  return whole.toString() + (fraction ? '.' + fraction : '');
}
export function utcInput(value: number | null): string {
  return value === null ? '' : new Date(value * 1000).toISOString().slice(0, 19);
}
export function parseUTC(value: string): number | null {
  if (value === '') return null;
  if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}(?::\d{2})?$/.test(value))
    throw new ApiError('invalid_request', 'Enter a valid UTC date and time.', 400);
  const text = value.length === 16 ? value + ':00' : value,
    n = Date.parse(text + 'Z') / 1000;
  integer(n, 'time', 0, 253_402_300_799);
  if (utcInput(n) !== text)
    throw new ApiError('invalid_request', 'Enter a valid UTC date and time.', 400);
  return n;
}

export function normalizePrice(value: string): string {
  if (value.length > 20 || !/^(0|[1-9][0-9]*)(?:\.[0-9]{1,3})?$/.test(value))
    throw new ApiError(
      'invalid_request',
      'Enter a positive price with at most three decimal places.',
      400,
    );
  const n = milli(value);
  if (n < 1n || n > maxPrice)
    throw new ApiError('invalid_request', 'Price is outside the supported range.', 400);
  return exchangeCost(value, '1')!;
}
