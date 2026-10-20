import { decoded, idempotentOptions, queryPath } from './api';
import type { CharityRole } from './charity';
import { normalizeNumberedPage, validateWindow } from './numberedPage';
import type { PageSize } from './pageNumbers';
import { array, boolean, decimal, invalidResponse, oneOf, record, string } from './wire';

export const BACKOFF_PERIODS = ['auto', 'five_hour', 'day', 'week', 'month'] as const;
export interface EndpointIdentity {
  connector_type: string;
  base_url: string;
}
export interface EndpointPolicySummary extends EndpointIdentity {
  revision: string;
  rule_count: string;
  key_count: string;
}
export interface EndpointBackoffRule {
  kind: 'code' | 'message';
  value: string;
  period: (typeof BACKOFF_PERIODS)[number];
}
export interface EndpointBackoffPolicy extends EndpointIdentity {
  revision: string;
  rules: EndpointBackoffRule[];
}
export interface EndpointPolicyInput extends EndpointIdentity {
  expected_revision: string;
  rules: EndpointBackoffRule[];
}
export interface CloudflareRoute {
  source_base_url: string;
  gateway_origin: string;
  credential_header: string;
  credential_query_parameter: string;
  retain_credential_header: boolean;
}
export interface CloudflareRoutes {
  enabled: boolean;
  routes: CloudflareRoute[];
}
const base = (role: CharityRole) => (role === 'admin' ? '/admin/api' : '/api/steward');
export const endpointPolicyKeys = (role: CharityRole) =>
  role === 'admin'
    ? (['admin', 'operations', 'endpoint-policies'] as const)
    : (['user', 'operations', 'steward', 'endpoint-policies'] as const);
export const endpointIdentityKey = (value: EndpointIdentity) =>
  JSON.stringify([value.connector_type, value.base_url]);
function identity(row: Record<string, unknown>): EndpointIdentity {
  return {
    connector_type: string(row.connector_type, 'connector type', { min: 1 }),
    base_url: string(row.base_url, 'endpoint URL', { min: 1 }),
  };
}
export function normalizeEndpointPolicy(value: unknown): EndpointBackoffPolicy {
  const row = record(value, ['connector_type', 'base_url', 'revision', 'rules'], 'endpoint policy');
  return {
    ...identity(row),
    revision: decimal(row.revision, 'policy revision'),
    rules: array(row.rules, 'backoff rules', 8).map((value) => {
      const rule = record(value, ['kind', 'value', 'period'], 'backoff rule');
      return {
        kind: oneOf(rule.kind, ['code', 'message'] as const, 'backoff match'),
        value: string(rule.value, 'backoff value', { min: 1 }),
        period: oneOf(rule.period, BACKOFF_PERIODS, 'backoff period'),
      };
    }),
  };
}
export function getEndpointPolicies(
  role: CharityRole,
  q: string,
  page: string,
  pageSize: PageSize,
  signal?: AbortSignal,
) {
  validateWindow(page, pageSize);
  return decoded(
    queryPath(`${base(role)}/endpoint-backoff-policies`, { q, page, page_size: pageSize }),
    (value) =>
      normalizeNumberedPage(
        value,
        'endpoint policies',
        (value): EndpointPolicySummary => {
          const row = record(
            value,
            ['connector_type', 'base_url', 'revision', 'rule_count', 'key_count'],
            'endpoint policy summary',
          );
          return {
            ...identity(row),
            revision: decimal(row.revision, 'policy revision'),
            rule_count: decimal(row.rule_count, 'rule count'),
            key_count: decimal(row.key_count, 'key count'),
          };
        },
        page,
        pageSize,
        endpointIdentityKey,
      ),
    { signal },
  );
}
export function getEndpointPolicy(
  role: CharityRole,
  endpoint: EndpointIdentity,
  signal?: AbortSignal,
) {
  return decoded(
    queryPath(`${base(role)}/endpoint-backoff-policy`, {
      connector_type: endpoint.connector_type,
      base_url: endpoint.base_url,
    }),
    (value) => {
      const result = normalizeEndpointPolicy(value);
      if (result.connector_type !== endpoint.connector_type) invalidResponse('endpoint connector');
      return result;
    },
    { signal },
  );
}
export function saveEndpointPolicy(
  role: CharityRole,
  input: EndpointPolicyInput,
  key: string,
  signal?: AbortSignal,
) {
  return decoded(
    `${base(role)}/endpoint-backoff-policy`,
    (value) => {
      const result = normalizeEndpointPolicy(value);
      if (endpointIdentityKey(result) !== endpointIdentityKey(input))
        invalidResponse('saved endpoint identity');
      return result;
    },
    idempotentOptions(key, { method: 'PUT', json: input, signal }),
  );
}
export function getEndpointPolicyOptions(role: CharityRole, signal?: AbortSignal) {
  return decoded(
    `${base(role)}/endpoint-backoff-options`,
    (value) => {
      const row = record(value, ['connector_types'], 'endpoint policy options');
      const types = array(row.connector_types, 'connector types', 64).map((type) =>
        string(type, 'connector type', { min: 1 }),
      );
      if (new Set(types).size !== types.length) invalidResponse('connector types');
      return types;
    },
    { signal },
  );
}
export function normalizeCloudflareRoutes(value: unknown): CloudflareRoutes {
  const root = record(value, ['enabled', 'routes'], 'loaded Cloudflare routes');
  return {
    enabled: boolean(root.enabled, 'routes enabled'),
    routes: array(root.routes, 'loaded routes', 32).map((value) => {
      const row = record(
        value,
        [
          'source_base_url',
          'gateway_origin',
          'credential_header',
          'credential_query_parameter',
          'retain_credential_header',
        ],
        'loaded route',
      );
      const origin = string(row.gateway_origin, 'gateway origin', { min: 1 });
      try {
        if (new URL(origin).origin !== origin) invalidResponse('gateway origin');
      } catch {
        invalidResponse('gateway origin');
      }
      return {
        source_base_url: string(row.source_base_url, 'source endpoint', { min: 1 }),
        gateway_origin: origin,
        credential_header: string(row.credential_header, 'credential header', { min: 1 }),
        credential_query_parameter: string(row.credential_query_parameter, 'credential query'),
        retain_credential_header: boolean(row.retain_credential_header, 'retained header'),
      };
    }),
  };
}
export function getCloudflareRoutes(role: CharityRole, signal?: AbortSignal) {
  return decoded(`${base(role)}/egress/cloudflare-routes`, normalizeCloudflareRoutes, { signal });
}
