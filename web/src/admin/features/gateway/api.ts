import { decoded, idempotentOptions } from '@shared/operations/api';
import {
  array,
  decimal,
  invalidResponse,
  record,
  string,
  unixSecond,
} from '@shared/operations/wire';
import {
  normalizeGatewayCapabilityPolicy,
  type GatewayCapabilityPolicy,
} from '@shared/gateway/capabilities';

export const gatewayCapabilityKeys = {
  root: ['admin', 'operations', 'gateway-capabilities'] as const,
};
export interface GatewayCapabilityEntry extends GatewayCapabilityPolicy {
  base_url: string;
  model: string;
}
export interface GatewayCapabilityRecord extends GatewayCapabilityEntry {
  id: string;
  revision: string;
  updated_at: number;
}
export type GatewayCapabilityIntent =
  | {
      action: 'create' | 'update';
      id?: string;
      expected_revision: string;
      entry: GatewayCapabilityEntry;
    }
  | { action: 'delete'; id: string; expected_revision: string };
export interface GatewayCapabilityDeleted {
  id: string;
  deleted: true;
}

export function normalizeGatewayCapability(value: unknown): GatewayCapabilityRecord {
  const row = record(
    value,
    [
      'id',
      'revision',
      'updated_at',
      'base_url',
      'model',
      'adapter',
      'efforts',
      'max_output_tokens',
      'storage',
      'cache',
    ],
    'Gateway capability',
  );
  return {
    id: string(row.id, 'Gateway capability ID', { min: 1, max: 128 }),
    revision: decimal(row.revision, 'Gateway capability revision', { positive: true }),
    updated_at: unixSecond(row.updated_at, 'Gateway capability update time'),
    base_url: string(row.base_url, 'Gateway base URL', { min: 1, max: 8192 }),
    model: string(row.model, 'Gateway upstream model', { min: 1, max: 512 }),
    ...normalizeGatewayCapabilityPolicy({
      adapter: row.adapter,
      efforts: row.efforts,
      max_output_tokens: row.max_output_tokens,
      storage: row.storage,
      cache: row.cache,
    }),
  };
}

const path = '/admin/api/gateway-model-capabilities';
export function getGatewayCapabilities(signal?: AbortSignal): Promise<GatewayCapabilityRecord[]> {
  return decoded(
    path,
    (value) => {
      const payload = record(value, ['data'], 'Gateway capability list');
      return array(payload.data, 'Gateway capability list', 128).map(normalizeGatewayCapability);
    },
    { signal },
  );
}
export function writeGatewayCapability(
  input: GatewayCapabilityIntent,
  key: string,
  signal?: AbortSignal,
): Promise<GatewayCapabilityRecord | GatewayCapabilityDeleted> {
  const target = input.action === 'create' ? path : `${path}/${encodeURIComponent(input.id ?? '')}`;
  const json =
    input.action === 'delete'
      ? { expected_revision: input.expected_revision }
      : { expected_revision: input.expected_revision, entry: input.entry };
  return decoded(
    target,
    (value) => {
      if (input.action !== 'delete') return normalizeGatewayCapability(value);
      const result = record(value, ['id', 'deleted'], 'Gateway capability deletion');
      if (result.deleted !== true) invalidResponse('Gateway capability deletion');
      return {
        id: string(result.id, 'Gateway capability ID', { min: 1, max: 128 }),
        deleted: true,
      };
    },
    idempotentOptions(key, {
      method: input.action === 'create' ? 'POST' : input.action === 'update' ? 'PUT' : 'DELETE',
      json,
      signal,
    }),
  );
}
