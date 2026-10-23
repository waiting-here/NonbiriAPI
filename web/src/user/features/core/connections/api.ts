import { DISCOVERY_FORMATS, normalizeNativeServices } from '@shared/connections';
import { queryPath } from '@shared/operations/api';
import { normalizePageMetadata } from '@shared/operations/pageNumbers';
import {
  array,
  boolean,
  decimal,
  decimalID,
  invalidResponse,
  nullableDecimalID,
  nullableUnixSecond,
  oneOf,
  optionalField,
  record,
  string,
  unixSecond,
  type WireRecord,
} from '@shared/operations/wire';
import { normalizeEndpoint } from '../normalizers';
import { coreRequest, operationHeaders } from '../request';
import type { NumberedPage, PageWindow } from '../pageTypes';
import type { Endpoint } from '../types';
import type {
  Connection,
  Credential,
  CredentialImpact,
  CredentialVersion,
  DiscoveryModel,
  DiscoverySource,
  DiscoverySourceInput,
  ImpactEntry,
  ImpactKind,
} from './types';

const times = (source: WireRecord) => ({
  created_at: unixSecond(source.created_at, 'creation time'),
  updated_at: unixSecond(source.updated_at, 'update time'),
});
const id = (source: WireRecord, name: string) => decimalID(source[name], name);
const revision = (source: WireRecord) => decimal(source.revision, 'revision', { positive: true });

export function normalizeConnection(value: unknown): Connection {
  const required = [
    'id',
    'name',
    'note',
    'revision',
    'entry_count',
    'credential_count',
    'created_at',
    'updated_at',
  ];
  const source = record(
    value,
    [...required, 'display_name', 'primary_entry'],
    'connection',
    required,
  );
  const name = string(source.name, 'connection name');
  return {
    id: id(source, 'id'),
    name,
    display_name:
      optionalField(source, 'display_name', (value) => string(value, 'connection display name')) ??
      name,
    primary_entry:
      optionalField(source, 'primary_entry', (value) => {
        if (value === null) return null;
        const entry = record(value, ['id', 'base_url', 'note'], 'primary entry');
        return {
          id: id(entry, 'id'),
          base_url: string(entry.base_url, 'primary entry URL'),
          note: string(entry.note, 'primary entry note'),
        };
      }) ?? null,
    note: string(source.note, 'connection note'),
    revision: revision(source),
    entry_count: decimal(source.entry_count, 'entry count'),
    credential_count: decimal(source.credential_count, 'credential count'),
    ...times(source),
  };
}
export function normalizeCredential(value: unknown): Credential {
  const source = record(
    value,
    [
      'id',
      'connection_id',
      'note',
      'enabled',
      'revision',
      'credential_version',
      'display_head',
      'display_tail',
      'created_at',
      'updated_at',
    ],
    'credential',
  );
  return {
    id: id(source, 'id'),
    connection_id: nullableDecimalID(source.connection_id, 'connection_id'),
    note: string(source.note, 'credential note'),
    enabled: boolean(source.enabled, 'credential enabled'),
    revision: revision(source),
    credential_version: id(source, 'credential_version'),
    display_head: string(source.display_head, 'credential head'),
    display_tail: string(source.display_tail, 'credential tail'),
    ...times(source),
  };
}
export function normalizeCredentialVersion(value: unknown): CredentialVersion {
  const source = record(
    value,
    [
      'credential_id',
      'credential_version',
      'enabled',
      'revision',
      'display_head',
      'display_tail',
      'created_at',
    ],
    'credential version',
  );
  return {
    credential_id: id(source, 'credential_id'),
    credential_version: id(source, 'credential_version'),
    enabled: boolean(source.enabled, 'version enabled'),
    revision: revision(source),
    display_head: string(source.display_head, 'version head'),
    display_tail: string(source.display_tail, 'version tail'),
    created_at: unixSecond(source.created_at, 'version creation'),
  };
}
export function normalizeDiscoverySource(value: unknown): DiscoverySource {
  const source = record(
    value,
    [
      'id',
      'name',
      'connection_id',
      'base_url',
      'list_format',
      'models_url',
      'credential_id',
      'credential_version',
      'revision',
      'discovery_source_revision',
      'enabled',
      'state',
      'checked_at',
      'safe_class',
      'safe_diag',
      'created_at',
      'updated_at',
      'credential_available',
    ],
    'discovery source',
    [
      'id',
      'name',
      'connection_id',
      'base_url',
      'list_format',
      'models_url',
      'credential_id',
      'credential_version',
      'revision',
      'discovery_source_revision',
      'enabled',
      'state',
      'checked_at',
      'safe_class',
      'safe_diag',
      'created_at',
      'updated_at',
    ],
  );
  return {
    ...(source.credential_available === undefined
      ? {}
      : { credential_available: boolean(source.credential_available, 'credential available') }),
    id: id(source, 'id'),
    name: string(source.name, 'source name'),
    connection_id: nullableDecimalID(source.connection_id, 'connection_id'),
    base_url: string(source.base_url, 'source URL'),
    list_format: oneOf(source.list_format, DISCOVERY_FORMATS, 'list format'),
    models_url: string(source.models_url, 'models URL'),
    credential_id: id(source, 'credential_id'),
    credential_version: id(source, 'credential_version'),
    revision: revision(source),
    discovery_source_revision: id(source, 'discovery_source_revision'),
    enabled: boolean(source.enabled, 'source enabled'),
    state: oneOf(
      source.state,
      ['unknown', 'checking', 'succeeded', 'failed'] as const,
      'source state',
    ),
    checked_at: nullableUnixSecond(source.checked_at, 'source check time'),
    safe_class: oneOf(
      source.safe_class,
      [
        'none',
        'auth',
        'rate_limit',
        'timeout',
        'protocol',
        'transport',
        'interrupted',
        'region',
        'unavailable',
      ] as const,
      'source safe class',
    ),
    safe_diag: string(source.safe_diag, 'source safe diagnostic'),
    ...times(source),
  };
}
export function normalizeDiscoveryModel(value: unknown): DiscoveryModel {
  const source = record(
    value,
    [
      'id',
      'upstream_model_id',
      'provider',
      'native_services',
      'service_evidence',
      'needs_service_selection',
    ],
    'discovery model',
  );
  return {
    id: id(source, 'id'),
    upstream_model_id: string(source.upstream_model_id, 'upstream model'),
    provider: string(source.provider, 'model provider'),
    native_services: normalizeNativeServices(source.native_services),
    service_evidence: oneOf(
      source.service_evidence,
      ['legacy', 'provider', 'preset', 'selected', 'unknown'] as const,
      'service evidence',
    ),
    needs_service_selection: boolean(
      source.needs_service_selection,
      'model needs service selection',
    ),
  };
}
function normalizeImpactEntry(value: unknown): ImpactEntry {
  const source = record(
    value,
    [
      'endpoint_id',
      'endpoint_key_id',
      'donation_id',
      'donation_key_id',
      'use_id',
      'discovery_source_id',
      'credential_version',
      'native_services',
      'state',
    ],
    'credential impact',
  );
  const optionalID = (name: string) => nullableDecimalID(source[name], name);
  return {
    endpoint_id: optionalID('endpoint_id'),
    endpoint_key_id: optionalID('endpoint_key_id'),
    donation_id: optionalID('donation_id'),
    donation_key_id: optionalID('donation_key_id'),
    use_id: optionalID('use_id'),
    discovery_source_id: optionalID('discovery_source_id'),
    credential_version: id(source, 'credential_version'),
    native_services: normalizeNativeServices(source.native_services),
    state: oneOf(source.state, ['current', 'approved', 'pending'] as const, 'impact state'),
  };
}
export function normalizeConnectionPage<T>(
  value: unknown,
  item: (value: unknown) => T,
): NumberedPage<T> {
  const source = record(value, ['data', 'next_cursor', 'pagination'], 'connection page');
  if (source.next_cursor !== null) invalidResponse('numbered connection cursor');
  return {
    data: array(source.data, 'page data').map(item),
    next_cursor: null,
    pagination: normalizePageMetadata(source.pagination),
  };
}
export function normalizeCredentialImpact(value: unknown): CredentialImpact {
  const source = record(
    value,
    ['data', 'next_cursor', 'pagination', 'kind', 'summary'],
    'credential impact page',
  );
  const summary = record(
    source.summary,
    ['endpoint_keys', 'donation_keys', 'discovery_sources'],
    'credential impact counts',
  );
  return {
    ...normalizeConnectionPage(value, normalizeImpactEntry),
    kind: oneOf(source.kind, ['personal', 'charity', 'discovery'] as const, 'impact kind'),
    summary: {
      endpoint_keys: decimal(summary.endpoint_keys, 'endpoint key count'),
      donation_keys: decimal(summary.donation_keys, 'donation key count'),
      discovery_sources: decimal(summary.discovery_sources, 'discovery source count'),
    },
  };
}

export async function connectionRead<T>(
  path: string,
  decode: (value: unknown) => T,
  signal?: AbortSignal,
): Promise<T> {
  return decode((await coreRequest(path, { signal })).payload);
}
export async function connectionWrite<T>(
  path: string,
  method: 'POST' | 'PATCH' | 'PUT' | 'DELETE',
  input: unknown,
  key: string,
  decode: (value: unknown) => T,
  signal?: AbortSignal,
): Promise<T> {
  return decode(
    (
      await coreRequest(path, {
        method,
        json: input,
        headers: operationHeaders({ idempotencyKey: key, actionId: key }),
        signal,
      })
    ).payload,
  );
}
export const resourcePath = (value: string) => encodeURIComponent(value);
export function connectionPage<T>(
  path: string,
  decode: (value: unknown) => T,
  window: PageWindow,
  filters: Record<string, string | boolean | undefined> = {},
  signal?: AbortSignal,
): Promise<NumberedPage<T>> {
  return connectionRead(
    queryPath(path, { page: window.page, page_size: window.pageSize, ...filters }),
    (value) => normalizeConnectionPage(value, decode),
    signal,
  );
}
export const connectionAPI = {
  list: (window: PageWindow, q: string, signal?: AbortSignal) =>
    connectionPage('/api/connections', normalizeConnection, window, { q }, signal),
  get: (value: string, signal?: AbortSignal) =>
    connectionRead('/api/connections/' + resourcePath(value), normalizeConnection, signal),
  entries: (
    value: string,
    window: PageWindow,
    q: string,
    signal?: AbortSignal,
  ): Promise<NumberedPage<Endpoint>> =>
    connectionPage(
      '/api/connections/' + resourcePath(value) + '/entries',
      normalizeEndpoint,
      window,
      { q },
      signal,
    ),
  credentials: (value: string, window: PageWindow, q: string, signal?: AbortSignal) =>
    connectionPage(
      '/api/connections/' + resourcePath(value) + '/credentials',
      normalizeCredential,
      window,
      { q },
      signal,
    ),
  credential: (value: string, signal?: AbortSignal) =>
    connectionRead('/api/credentials/' + resourcePath(value), normalizeCredential, signal),
  versions: (value: string, window: PageWindow, signal?: AbortSignal) =>
    connectionPage(
      '/api/credentials/' + resourcePath(value) + '/versions',
      normalizeCredentialVersion,
      window,
      {},
      signal,
    ),
  impact: (value: string, kind: ImpactKind, window: PageWindow, signal?: AbortSignal) =>
    connectionRead(
      queryPath('/api/credentials/' + resourcePath(value) + '/impact', {
        kind,
        page: window.page,
        page_size: window.pageSize,
      }),
      normalizeCredentialImpact,
      signal,
    ),
  sources: (value: string, window: PageWindow, q: string, signal?: AbortSignal) =>
    connectionPage(
      '/api/connections/' + resourcePath(value) + '/discovery-sources',
      normalizeDiscoverySource,
      window,
      { q },
      signal,
    ),
  source: (value: string, signal?: AbortSignal) =>
    connectionRead(
      '/api/discovery-sources/' + resourcePath(value),
      normalizeDiscoverySource,
      signal,
    ),
  models: (
    value: string,
    window: PageWindow,
    q: string,
    state: 'current' | 'stale' | undefined,
    signal?: AbortSignal,
  ) =>
    connectionPage(
      '/api/discovery-sources/' + resourcePath(value) + '/models',
      normalizeDiscoveryModel,
      window,
      { q, state },
      signal,
    ),
  create: (input: { name: string; note?: string }, key: string, signal?: AbortSignal) =>
    connectionWrite('/api/connections', 'POST', input, key, normalizeConnection, signal),
  createCredential: (
    value: string,
    input: { secret: string; note?: string; ownership_confirmed: true },
    key: string,
    signal?: AbortSignal,
  ) =>
    connectionWrite(
      '/api/connections/' + resourcePath(value) + '/credentials',
      'POST',
      input,
      key,
      normalizeCredential,
      signal,
    ),
  createSource: (value: string, input: DiscoverySourceInput, key: string, signal?: AbortSignal) =>
    connectionWrite(
      '/api/connections/' + resourcePath(value) + '/discovery-sources',
      'POST',
      input,
      key,
      normalizeDiscoverySource,
      signal,
    ),
};
