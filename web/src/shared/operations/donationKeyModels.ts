import { decoded, queryPath } from './api';
import { charityScopePath } from './charityScope';
import { type CharityRole } from './charity';
import { normalizeNumberedPage, validateWindow, invalidRequest } from './numberedPage';
import { type PageSize } from './pageNumbers';
import {
  boolean,
  decimal,
  decimalID,
  integer,
  invalidResponse,
  oneOf,
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
) {
  validateWindow(page, size);
  return decoded(
    charityScopePath(
      queryPath(path(role, donationId, keyId), { page, page_size: size }),
      charityModelID,
    ),
    (value) =>
      normalizeNumberedPage<DonationKeyModel>(
        value,
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
            full_name: string(row.full_name, 'model name', { min: 1, max: 140, bytes: 560 }),
            enabled: boolean(row.enabled, 'model enabled'),
            binding_count: count,
            available_binding_count: available,
          };
        },
        page,
        size,
        (entry) => entry.model_id,
      ),
    { signal },
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
            upstream_model_id: string(row.upstream_model_id, 'upstream model id', {
              min: 1,
              max: 512,
              bytes: 2048,
            }),
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
