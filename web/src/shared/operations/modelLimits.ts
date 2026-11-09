import { decoded, idempotentOptions, queryPath } from './api';
import { charityScopePath } from './charityScope';
import { normalizeNumberedPage } from './numberedPage';
import type { PageSize } from './pageNumbers';
import { decimal, decimalID, integer, record, string } from './wire';
import {
  recurringLimitsBase,
  type RecurringLimitRole,
  type RecurringLimitsWriteReceipt,
} from './recurringLimits';

export type ModelMetric = 'tokens' | 'input_tokens' | 'output_tokens';
export type ModelAmounts = Record<ModelMetric, string>;
export type ModelBudgets = Record<ModelMetric, string | null>;
export interface ModelLimitView {
  upstream_model_id: string;
  limits: ModelBudgets;
  used: ModelAmounts;
  reserved: ModelAmounts;
  remaining: ModelBudgets;
  unattributed_total_tokens: string;
  tracking_started_at: number | null;
}
export interface ModelLimitPayload extends ModelBudgets {
  upstream_model_id: string;
  expected_revision: string;
}

function path(role: RecurringLimitRole, donationId: string, keyId: string) {
  return `${recurringLimitsBase(role)}/donations/${decimalID(donationId, 'donation')}/keys/${decimalID(keyId, 'key')}/model-limits`;
}
function amounts(value: unknown, nullable: true): ModelBudgets;
function amounts(value: unknown, nullable: false): ModelAmounts;
function amounts(value: unknown, nullable: boolean) {
  const row = record(value, ['tokens', 'input_tokens', 'output_tokens'], 'model token amounts');
  const count = (metric: ModelMetric) =>
    nullable && row[metric] === null ? null : decimal(row[metric], metric, { u128: true });
  return {
    tokens: count('tokens'),
    input_tokens: count('input_tokens'),
    output_tokens: count('output_tokens'),
  };
}
function view(value: unknown): ModelLimitView {
  const row = record(
    value,
    [
      'upstream_model_id',
      'limits',
      'used',
      'reserved',
      'remaining',
      'unattributed_total_tokens',
      'tracking_started_at',
    ],
    'model quota',
  );
  return {
    upstream_model_id: string(row.upstream_model_id, 'upstream model', { min: 1 }),
    limits: amounts(row.limits, true),
    used: amounts(row.used, false),
    reserved: amounts(row.reserved, false),
    remaining: amounts(row.remaining, true),
    unattributed_total_tokens: decimal(row.unattributed_total_tokens, 'unattributed tokens', {
      u128: true,
    }),
    tracking_started_at:
      row.tracking_started_at === null
        ? null
        : integer(row.tracking_started_at, 'tracking start', 0, 253402300799),
  };
}
export function getModelLimits(
  role: RecurringLimitRole,
  donationId: string,
  keyId: string,
  page: string,
  pageSize: PageSize,
  query: string,
  signal?: AbortSignal,
  charityModelID?: string,
  upstreamModelID?: string,
) {
  return decoded(
    charityScopePath(
      queryPath(path(role, donationId, keyId), {
        page,
        page_size: pageSize,
        q: query || undefined,
        upstream_model_id: upstreamModelID,
      }),
      charityModelID,
    ),
    (value) => {
      const row = record(
        value,
        ['donation_id', 'key_id', 'donation_revision', 'data', 'next_cursor', 'pagination'],
        'model limits page',
      );
      return {
        ...normalizeNumberedPage(
          row,
          'model limits',
          view,
          page,
          pageSize,
          (item) => item.upstream_model_id,
        ),
        donation_revision: decimal(row.donation_revision, 'donation revision', { positive: true }),
      };
    },
    { signal },
  );
}
export function putModelLimits(
  role: RecurringLimitRole,
  donationId: string,
  keyId: string,
  payload: ModelLimitPayload,
  idempotencyKey: string,
  charityModelID?: string,
): Promise<RecurringLimitsWriteReceipt> {
  return decoded(
    charityScopePath(path(role, donationId, keyId), charityModelID),
    (value) => {
      const row = record(
        value,
        ['donation_id', 'key_id', 'donation_revision'],
        'model limits receipt',
      );
      return {
        donation_id: decimalID(row.donation_id, 'donation'),
        key_id: decimalID(row.key_id, 'key'),
        donation_revision: decimal(row.donation_revision, 'revision', { positive: true }),
      };
    },
    idempotentOptions(idempotencyKey, { method: 'PUT', json: payload }),
  );
}
