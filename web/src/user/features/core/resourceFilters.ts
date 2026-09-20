import { ApiError } from '@shared/query/http';
import { validateScalarInput } from './normalizers';

export type ResourceListKind = 'endpoints' | 'keys' | 'models';
export type ResourceFilters = Partial<
  Record<
    | 'q'
    | 'connector_type'
    | 'source'
    | 'state'
    | 'enabled'
    | 'donated'
    | 'suspension_state'
    | 'provider'
    | 'route_strategy'
    | 'connection_state',
    string
  >
>;

export const resourceFilterFields = {
  endpoints: ['q', 'connector_type', 'source', 'state'],
  keys: ['q', 'enabled', 'donated', 'suspension_state'],
  models: ['q', 'provider', 'route_strategy', 'connection_state'],
} as const;

const choices: Partial<Record<keyof ResourceFilters, readonly string[]>> = {
  source: ['mainstream', 'custom'],
  state: ['available', 'endpoint_disabled', 'no_keys', 'no_usable_key'],
  enabled: ['true', 'false'],
  donated: ['true', 'false'],
  suspension_state: ['none', 'security_processing'],
  route_strategy: ['ordered', 'random'],
  connection_state: ['available', 'unavailable', 'unconfigured'],
};

export function canonicalResourceFilters(
  kind: ResourceListKind,
  value: ResourceFilters,
): ResourceFilters {
  const fields: readonly string[] = resourceFilterFields[kind];
  if (
    !value ||
    typeof value !== 'object' ||
    Array.isArray(value) ||
    Object.keys(value).some((field) => !fields.includes(field))
  )
    throw new ApiError('invalid_request', 'Invalid resource filters.', 400);
  const out: ResourceFilters = {};
  for (const field of resourceFilterFields[kind]) {
    const raw = value[field];
    if (raw === undefined || raw === '') continue;
    const max = field === 'q' ? (kind === 'models' ? 512 : 128) : 64;
    const text = validateScalarInput(raw, max, 'resource filter', true);
    const canonical = field === 'q' ? text.trim() : text;
    if (!canonical) continue;
    if (
      (choices[field] && !choices[field]?.includes(canonical)) ||
      (field === 'provider' && (canonical.trim() !== canonical || canonical.startsWith('[公益]')))
    )
      throw new ApiError('invalid_request', 'Invalid resource filter.', 400);
    out[field] = canonical;
  }
  return out;
}

export function resourceFilterIdentity(kind: ResourceListKind, value: ResourceFilters): string {
  return new URLSearchParams(canonicalResourceFilters(kind, value)).toString();
}
