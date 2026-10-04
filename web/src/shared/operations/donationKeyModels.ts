import { decoded, idempotentOptions, queryPath } from './api';
import { charityScopePath } from './charityScope';
import { type CharityRole } from './charity';
import {
  normalizeNumberedPage,
  validateWindow,
  invalidRequest,
  type NumberedPage,
} from './numberedPage';
import { type PageSize } from './pageNumbers';
import {
  boolean,
  array,
  decimal,
  decimalID,
  integer,
  invalidResponse,
  oneOf,
  nullableDecimal,
  nullableDecimalID,
  record,
  string,
} from './wire';

export const donationBindingStates = [
  'available',
  'unavailable',
  'ended',
  'expired',
  'pending',
  'feature_disabled',
  'model_disabled',
  'disabled',
  'suspended',
] as const;
export interface DonationKeyModel {
  model_id: string;
  full_name: string;
  enabled: boolean;
  binding_count: string;
  available_binding_count: string;
}
export interface DonationKeyModelBinding {
  binding_id: string;
  upstream_model_id: string;
  ord: number;
  state: (typeof donationBindingStates)[number];
}
export interface ManualCandidate {
  upstream_model_id: string;
  display_name: string;
  source: 'automatic' | 'manual' | 'both';
  verified: boolean;
  manual_entry_id: string | null;
  manual_entry_revision: string | null;
}
export interface ManualCatalog {
  entries: ManualCandidate[];
  manual_catalog_revision: string;
}
function normalizeCandidate(value: unknown): ManualCandidate {
  const row = record(
    value,
    [
      'upstream_model_id',
      'display_name',
      'source',
      'verified',
      'manual_entry_id',
      'manual_entry_revision',
    ],
    'candidate model',
  );
  return {
    upstream_model_id: string(row.upstream_model_id, 'upstream model', { min: 1 }),
    display_name: string(row.display_name, 'candidate display', { min: 1 }),
    source: oneOf(row.source, ['automatic', 'manual', 'both'] as const, 'candidate source'),
    verified: boolean(row.verified, 'candidate verified'),
    manual_entry_id: nullableDecimalID(row.manual_entry_id, 'manual entry id'),
    manual_entry_revision: nullableDecimal(row.manual_entry_revision, 'manual entry revision'),
  };
}
function normalizeCatalog(value: unknown): ManualCatalog {
  const row = record(value, ['entries', 'manual_catalog_revision'], 'manual catalog');
  return {
    entries: array(row.entries, 'manual entries', 100).map(normalizeCandidate),
    manual_catalog_revision: decimal(row.manual_catalog_revision, 'manual catalog revision', {
      positive: true,
    }),
  };
}

function path(role: CharityRole, donationId: string, keyId: string): string {
  if (role !== 'admin' && role !== 'steward') invalidRequest();
  const base = role === 'admin' ? '/admin/api' : '/api/steward';
  return `${base}/donations/${decimalID(donationId, 'donation id')}/keys/${decimalID(keyId, 'donation key id')}/models`;
}

export function getDonationKeyModels(
  role: CharityRole,
  donationId: string,
  keyId: string,
  page: string,
  size: PageSize,
  signal?: AbortSignal,
  charityModelID?: string,
  q?: string,
) {
  validateWindow(page, size);
  return decoded(
    charityScopePath(
      queryPath(path(role, donationId, keyId), { page, page_size: size, q }),
      charityModelID,
    ),
    (value) => {
      const root = record(
        value,
        [
          'data',
          'next_cursor',
          'pagination',
          'candidates',
          'candidates_pagination',
          'manual_catalog_revision',
        ],
        'donation key models',
        ['data', 'next_cursor', 'pagination'],
      );
      const models = normalizeNumberedPage<DonationKeyModel>(
        { data: root.data, next_cursor: root.next_cursor, pagination: root.pagination },
        'donation key models',
        (entry) => {
          const row = record(
            entry,
            ['model_id', 'full_name', 'enabled', 'binding_count', 'available_binding_count'],
            'donation key model',
          );
          const count = decimal(row.binding_count, 'binding count');
          const available = decimal(row.available_binding_count, 'available binding count');
          if (BigInt(count) < 1n || BigInt(available) > BigInt(count))
            invalidResponse('binding counts');
          return {
            model_id: decimalID(row.model_id, 'model id'),
            full_name: string(row.full_name, 'model name', { min: 1 }),
            enabled: boolean(row.enabled, 'model enabled'),
            binding_count: count,
            available_binding_count: available,
          };
        },
        page,
        size,
        (entry) => entry.model_id,
      );
      const candidates: NumberedPage<ManualCandidate> | undefined =
        root.candidates === undefined
          ? undefined
          : normalizeNumberedPage(
              { data: root.candidates, next_cursor: null, pagination: root.candidates_pagination },
              'candidate models',
              normalizeCandidate,
              page,
              size,
              (entry) => entry.upstream_model_id,
            );
      return {
        ...models,
        candidates,
        manual_catalog_revision:
          root.manual_catalog_revision === undefined
            ? undefined
            : decimal(root.manual_catalog_revision, 'manual catalog revision', { positive: true }),
      };
    },
    { signal },
  );
}

export function addDonationManualModels(
  role: CharityRole,
  donationId: string,
  keyId: string,
  entries: string[],
  revision: string,
  operationKey: string,
  signal?: AbortSignal,
  charityModelID?: string,
) {
  return decoded(
    charityScopePath(`${path(role, donationId, keyId)}/manual`, charityModelID),
    normalizeCatalog,
    idempotentOptions(operationKey, {
      method: 'POST',
      json: { entries, expected_manual_catalog_revision: revision },
      signal,
    }),
  );
}
export function removeDonationManualModel(
  role: CharityRole,
  donationId: string,
  keyId: string,
  entryId: string,
  revision: string,
  operationKey: string,
  signal?: AbortSignal,
  charityModelID?: string,
) {
  return decoded(
    charityScopePath(
      `${path(role, donationId, keyId)}/manual/${decimalID(entryId, 'manual entry id')}`,
      charityModelID,
    ),
    normalizeCatalog,
    idempotentOptions(operationKey, {
      method: 'DELETE',
      json: { expected_manual_catalog_revision: revision },
      signal,
    }),
  );
}

export function getDonationKeyModelBindings(
  role: CharityRole,
  donationId: string,
  keyId: string,
  modelId: string,
  page: string,
  size: PageSize,
  signal?: AbortSignal,
  charityModelID?: string,
) {
  validateWindow(page, size);
  return decoded(
    charityScopePath(
      queryPath(`${path(role, donationId, keyId)}/${decimalID(modelId, 'model id')}/bindings`, {
        page,
        page_size: size,
      }),
      charityModelID,
    ),
    (value) =>
      normalizeNumberedPage<DonationKeyModelBinding>(
        value,
        'donation key model bindings',
        (entry) => {
          const row = record(
            entry,
            ['binding_id', 'upstream_model_id', 'ord', 'state'],
            'donation key model binding',
          );
          return {
            binding_id: decimalID(row.binding_id, 'binding id'),
            upstream_model_id: string(row.upstream_model_id, 'upstream model id', { min: 1 }),
            ord: integer(row.ord, 'binding order', 0, 255),
            state: oneOf(row.state, donationBindingStates, 'binding state'),
          };
        },
        page,
        size,
        (entry) => entry.binding_id,
      ),
    { signal },
  );
}
