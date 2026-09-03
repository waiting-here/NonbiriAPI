import { ApiError, isApiError } from '@shared/query/http';
import {
  canonicalBaseURLPreview,
  canonicalCandidateFilters,
  normalizeAuthorizationURL,
  normalizeBindingCandidatePage,
  normalizeBindingsResponse,
  normalizeCallerKeyAuthority,
  normalizeCallerKeySecret,
  normalizeCatalogView,
  normalizeDiscoveryAccepted,
  normalizeEndpoint,
  normalizeEndpointCreateOptions,
  normalizeEndpointKey,
  normalizeEndpointKeyPage,
  normalizeEndpointPage,
  normalizeHomeAnnouncementPage,
  normalizeHomeCheckinResult,
  normalizeHomeCheckinStatus,
  normalizeHomeGameSummary,
  normalizeManualEntriesResponse,
  normalizeManualUpdateResponse,
  normalizeModel,
  normalizeModelPage,
  normalizeUserEnvelope,
  validateEndpointSecret,
  validateLogicalName,
  validateManualValue,
  validateMainstreamChannelID,
  validatePaginationCursor,
  validatePersonalProviderName,
  validateResourceId,
  validateRevisionInput,
  validateScalarInput,
} from './normalizers';
import { coreRawRequest, coreRequest, operationHeaders } from './request';
import {
  CONNECTOR_TYPES,
  type AccountAuthority,
  type AccountExportAttachment,
  type BindingCandidate,
  type BindingReplacement,
  type BindingSelection,
  type BindingsResponse,
  type CallerKeyAuthority,
  type CallerKeySecret,
  type CandidateFilters,
  type CatalogView,
  type DiscoveryAccepted,
  type Endpoint,
  type EndpointCreateInput,
  type EndpointCreateOptions,
  type EndpointKey,
  type EndpointKeyCreateInput,
  type EndpointKeyPatchInput,
  type EndpointPatchInput,
  type ExplicitLanguage,
  type HomeAnnouncementSummary,
  type HomeCheckinResult,
  type HomeCheckinStatus,
  type HomeGameSummary,
  type ManualEntriesResponse,
  type ManualUpdateResponse,
  type Model,
  type ModelCreateInput,
  type ModelPatchInput,
  type OperationIdentity,
  type Page,
  type ConnectorType,
  type UserEnvelope,
} from './types';

function pathID(value: string, label: string): string {
  return encodeURIComponent(validateResourceId(value, label));
}

function expectedStatus(actual: number, expected: number, label: string): void {
  if (actual !== expected)
    throw new ApiError(
      'invalid_response',
      `The server returned an invalid ${label} status.`,
      actual,
    );
}

function exactInput(
  value: unknown,
  required: readonly string[],
  optional: readonly string[],
  label: string,
): Record<string, unknown> {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) {
    throw new ApiError('invalid_request', `Invalid ${label}.`, 400);
  }
  const record = value as Record<string, unknown>;
  const allowed = new Set([...required, ...optional]);
  if (
    Object.keys(record).some((key) => !allowed.has(key)) ||
    required.some((key) => !Object.hasOwn(record, key) || record[key] === undefined)
  ) {
    throw new ApiError('invalid_request', `Invalid ${label}.`, 400);
  }
  return record;
}

function exactBooleanInput(value: unknown, label: string): boolean {
  if (typeof value !== 'boolean') throw new ApiError('invalid_request', `Invalid ${label}.`, 400);
  return value;
}

function revisionField(value: unknown, label: string, positive = false): string {
  if (typeof value !== 'string') throw new ApiError('invalid_request', `Invalid ${label}.`, 400);
  return validateRevisionInput(value, label, positive);
}

function connectorInput(value: unknown): ConnectorType {
  if (typeof value !== 'string' || !CONNECTOR_TYPES.includes(value as ConnectorType)) {
    throw new ApiError('invalid_request', 'Invalid connector type.', 400);
  }
  return value as ConnectorType;
}

function canonicalEndpointURLInput(value: unknown): string {
  if (typeof value !== 'string')
    throw new ApiError('invalid_request', 'Invalid endpoint URL.', 400);
  const canonical = canonicalBaseURLPreview(value);
  if (canonical !== value)
    throw new ApiError('invalid_request', 'Invalid canonical endpoint URL.', 400);
  return canonical;
}

function pageQuery(cursor?: string, limit = 50): string {
  if (!Number.isSafeInteger(limit) || limit < 1 || limit > 100) {
    throw new ApiError('invalid_request', 'Invalid page limit.', 400);
  }
  const params = new URLSearchParams({ limit: String(limit) });
  if (cursor) params.set('cursor', validatePaginationCursor(cursor));
  return params.toString();
}

export async function getSession(signal?: AbortSignal): Promise<UserEnvelope> {
  const response = await coreRequest('/api/session', { signal });
  expectedStatus(response.status, 200, 'session');
  return normalizeUserEnvelope(response.payload);
}

export async function getMe(signal?: AbortSignal): Promise<UserEnvelope> {
  const response = await coreRequest('/api/me', { signal });
  expectedStatus(response.status, 200, 'profile');
  return normalizeUserEnvelope(response.payload);
}

export async function getHomeCheckinStatus(signal?: AbortSignal): Promise<HomeCheckinStatus> {
  const response = await coreRequest('/api/checkin', { signal });
  expectedStatus(response.status, 200, 'check-in status');
  return normalizeHomeCheckinStatus(response.payload);
}

export async function submitHomeCheckin(signal?: AbortSignal): Promise<HomeCheckinResult> {
  const response = await coreRequest('/api/checkin', { method: 'POST', signal });
  expectedStatus(response.status, 200, 'check-in');
  return normalizeHomeCheckinResult(response.payload);
}

export async function getHomeGameSummary(signal?: AbortSignal): Promise<HomeGameSummary[]> {
  const response = await coreRequest('/api/home/game-summary', { signal });
  expectedStatus(response.status, 200, 'home game summary');
  return normalizeHomeGameSummary(response.payload);
}

export async function getHomeAnnouncements(
  signal?: AbortSignal,
): Promise<HomeAnnouncementSummary[]> {
  const response = await coreRequest('/api/announcements?limit=20', { signal });
  expectedStatus(response.status, 200, 'home announcements');
  return normalizeHomeAnnouncementPage(response.payload).data;
}

export async function patchLanguage(
  language: ExplicitLanguage,
  operation: OperationIdentity,
  signal?: AbortSignal,
): Promise<UserEnvelope> {
  if (language !== 'zh' && language !== 'en')
    throw new ApiError('invalid_request', 'Invalid account language.', 400);
  const response = await coreRequest('/api/me', {
    method: 'PATCH',
    headers: operationHeaders(operation),
    json: { lang: language },
    signal,
  });
  expectedStatus(response.status, 200, 'profile update');
  return normalizeUserEnvelope(response.payload);
}

export async function beginElevation(signal?: AbortSignal): Promise<string> {
  const response = await coreRequest('/api/auth/elevate', { method: 'POST', signal });
  expectedStatus(response.status, 200, 'elevation');
  return normalizeAuthorizationURL(response.payload);
}

const MAX_ACCOUNT_EXPORT_BYTES = 16 * 1024 * 1024;
const ACCOUNT_EXPORT_KEYS = [
  'schema_version',
  'generated_at',
  'user',
  'endpoints',
  'catalog_pairs',
  'models',
  'caller_key',
  'usage',
  'log_summary',
  'issues',
  'credit_ledger',
  'welfare_claims',
  'thursday',
  'donations',
  'charity',
  'fishing',
  'linklink',
  'rps',
] as const;
const ELEVATED_TOKEN = /^[A-Za-z0-9._-]{8,512}$/;

function accountIdentity(value: string): string {
  return validateRevisionInput(value, 'account id', true);
}

function elevatedHeaders(token: string): HeadersInit {
  if (!ELEVATED_TOKEN.test(token))
    throw new ApiError('invalid_request', 'Invalid elevated capability.', 400);
  return { 'X-Elevated-Token': token };
}

async function boundedAccountExport(response: Response): Promise<Uint8Array> {
  const declaredLength = response.headers.get('Content-Length');
  if (declaredLength !== null) {
    if (!/^(0|[1-9][0-9]*)$/.test(declaredLength))
      throw new ApiError('invalid_response', 'The server returned an invalid export length.', 200);
    const length = Number(declaredLength);
    if (!Number.isSafeInteger(length) || length > MAX_ACCOUNT_EXPORT_BYTES)
      throw new ApiError('invalid_response', 'The server returned an oversized export.', 200);
  }
  if (!response.body)
    throw new ApiError('invalid_response', 'The server returned an empty export.', 200);

  const reader = response.body.getReader();
  const chunks: Uint8Array[] = [];
  let total = 0;
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    if (!value) continue;
    total += value.byteLength;
    if (total > MAX_ACCOUNT_EXPORT_BYTES) {
      await reader.cancel();
      throw new ApiError('invalid_response', 'The server returned an oversized export.', 200);
    }
    chunks.push(value);
  }
  if (total === 0)
    throw new ApiError('invalid_response', 'The server returned an empty export.', 200);
  const bytes = new Uint8Array(total);
  let offset = 0;
  for (const chunk of chunks) {
    bytes.set(chunk, offset);
    offset += chunk.byteLength;
  }
  return bytes;
}

function validateAccountExport(bytes: Uint8Array): void {
  let value: unknown;
  try {
    value = JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(bytes)) as unknown;
  } catch {
    throw new ApiError('invalid_response', 'The server returned an invalid account export.', 200);
  }
  if (value === null || typeof value !== 'object' || Array.isArray(value))
    throw new ApiError('invalid_response', 'The server returned an invalid account export.', 200);
  const record = value as Record<string, unknown>;
  const expected = new Set<string>(ACCOUNT_EXPORT_KEYS);
  if (
    record.schema_version !== 4 ||
    Object.keys(record).length !== ACCOUNT_EXPORT_KEYS.length ||
    Object.keys(record).some((key) => !expected.has(key))
  ) {
    throw new ApiError('invalid_response', 'The server returned an invalid account export.', 200);
  }
}

export async function exportAccountV4(
  accountId: string,
  elevatedToken: string,
  signal?: AbortSignal,
): Promise<AccountExportAttachment> {
  accountIdentity(accountId);
  const response = await coreRawRequest('/api/account/export', {
    method: 'POST',
    headers: elevatedHeaders(elevatedToken),
    signal,
  });
  expectedStatus(response.status, 200, 'account export');
  const contentType = response.headers.get('Content-Type')?.toLowerCase() ?? '';
  const disposition = response.headers.get('Content-Disposition') ?? '';
  if (
    !contentType.startsWith('application/json') ||
    disposition !== 'attachment; filename="nonbiriapi-account-export-v4.json"'
  ) {
    throw new ApiError('invalid_response', 'The server returned invalid export metadata.', 200);
  }
  const bytes = await boundedAccountExport(response);
  validateAccountExport(bytes);
  const buffer = new ArrayBuffer(bytes.byteLength);
  new Uint8Array(buffer).set(bytes);
  return {
    blob: new Blob([buffer], { type: 'application/json' }),
    schemaVersion: 4,
  };
}

export async function deleteCurrentAccount(
  accountId: string,
  elevatedToken: string,
  confirmation: 'DELETE',
  signal?: AbortSignal,
): Promise<void> {
  accountIdentity(accountId);
  if (confirmation !== 'DELETE')
    throw new ApiError('invalid_request', 'Invalid account deletion confirmation.', 400);
  const response = await coreRequest('/api/account/delete', {
    method: 'POST',
    headers: elevatedHeaders(elevatedToken),
    json: { confirm: confirmation },
    signal,
  });
  expectedStatus(response.status, 204, 'account deletion');
}

export async function readAccountAuthority(
  accountId: string,
  signal?: AbortSignal,
): Promise<AccountAuthority> {
  const expectedAccount = accountIdentity(accountId);
  try {
    const response = await getSession(signal);
    if (response.user.id !== expectedAccount)
      throw new ApiError('invalid_response', 'The server returned a different account.', 200);
    return 'active';
  } catch (error) {
    // A successful synchronous deletion revokes the browser session in the
    // same transaction. After an unknown delete response, an unauthorized
    // session is therefore the only available authoritative terminal signal.
    if (isApiError(error) && error.status === 401) return 'deleted';
    throw error;
  }
}

export async function listEndpoints(
  cursor?: string,
  signal?: AbortSignal,
): Promise<Page<Endpoint>> {
  const response = await coreRequest(`/api/endpoints?${pageQuery(cursor)}`, { signal });
  expectedStatus(response.status, 200, 'endpoint list');
  return normalizeEndpointPage(response.payload);
}

export async function getEndpoint(endpointId: string, signal?: AbortSignal): Promise<Endpoint> {
  const response = await coreRequest(`/api/endpoints/${pathID(endpointId, 'endpoint id')}`, {
    signal,
  });
  expectedStatus(response.status, 200, 'endpoint');
  const endpoint = normalizeEndpoint(response.payload);
  if (endpoint.id !== endpointId)
    throw new ApiError('invalid_response', 'The server returned a different endpoint.', 200);
  return endpoint;
}

export async function getEndpointCreateOptions(
  signal?: AbortSignal,
): Promise<EndpointCreateOptions> {
  const response = await coreRequest('/api/endpoint-create-options', { signal });
  expectedStatus(response.status, 200, 'endpoint creation options');
  return normalizeEndpointCreateOptions(response.payload);
}

function endpointCreatePayload(input: EndpointCreateInput): EndpointCreateInput {
  const root = exactInput(
    input,
    ['source'],
    ['channel_id', 'connector_type', 'base_url', 'note', 'enabled'],
    'endpoint creation input',
  );
  if (root.source === 'mainstream') {
    const record = exactInput(
      input,
      ['source', 'channel_id', 'note', 'enabled'],
      [],
      'mainstream endpoint creation input',
    );
    if (typeof record.channel_id !== 'string') {
      throw new ApiError('invalid_request', 'Invalid mainstream channel id.', 400);
    }
    return {
      source: 'mainstream',
      channel_id: validateMainstreamChannelID(record.channel_id),
      note: validateScalarInput(record.note, 1_024, 'endpoint note'),
      enabled: exactBooleanInput(record.enabled, 'endpoint enabled state'),
    };
  }
  if (root.source === 'custom') {
    const record = exactInput(
      input,
      ['source', 'connector_type', 'base_url', 'note', 'enabled'],
      [],
      'custom endpoint creation input',
    );
    return {
      source: 'custom',
      connector_type: connectorInput(record.connector_type),
      base_url: canonicalEndpointURLInput(record.base_url),
      note: validateScalarInput(record.note, 1_024, 'endpoint note'),
      enabled: exactBooleanInput(record.enabled, 'endpoint enabled state'),
    };
  }
  throw new ApiError('invalid_request', 'Invalid endpoint creation source.', 400);
}

export async function createEndpoint(
  input: EndpointCreateInput,
  operation: OperationIdentity,
  signal?: AbortSignal,
): Promise<Endpoint> {
  const payload = endpointCreatePayload(input);
  const response = await coreRequest('/api/endpoints', {
    method: 'POST',
    headers: operationHeaders(operation),
    json: payload,
    signal,
  });
  expectedStatus(response.status, 201, 'endpoint creation');
  return normalizeEndpoint(response.payload);
}

export async function patchEndpoint(
  endpointId: string,
  input: EndpointPatchInput,
  operation: OperationIdentity,
  signal?: AbortSignal,
): Promise<Endpoint> {
  const record = exactInput(
    input,
    ['expected_revision'],
    ['note', 'enabled'],
    'endpoint update input',
  );
  if (!Object.hasOwn(record, 'note') && !Object.hasOwn(record, 'enabled')) {
    throw new ApiError('invalid_request', 'Invalid endpoint update input.', 400);
  }
  const payload: EndpointPatchInput = {
    expected_revision: revisionField(record.expected_revision, 'endpoint revision', true),
    ...(Object.hasOwn(record, 'note')
      ? { note: validateScalarInput(record.note, 1_024, 'endpoint note') }
      : {}),
    ...(Object.hasOwn(record, 'enabled')
      ? { enabled: exactBooleanInput(record.enabled, 'endpoint enabled state') }
      : {}),
  };
  const response = await coreRequest(`/api/endpoints/${pathID(endpointId, 'endpoint id')}`, {
    method: 'PATCH',
    headers: operationHeaders(operation),
    json: payload,
    signal,
  });
  expectedStatus(response.status, 200, 'endpoint update');
  const endpoint = normalizeEndpoint(response.payload);
  if (endpoint.id !== endpointId)
    throw new ApiError('invalid_response', 'The server returned a different endpoint.', 200);
  return endpoint;
}

export async function deleteEndpoint(
  endpointId: string,
  expectedRevision: string,
  operation: OperationIdentity,
  signal?: AbortSignal,
): Promise<void> {
  validateRevisionInput(expectedRevision, 'endpoint revision', true);
  const response = await coreRequest(`/api/endpoints/${pathID(endpointId, 'endpoint id')}`, {
    method: 'DELETE',
    headers: operationHeaders(operation),
    json: { expected_revision: expectedRevision },
    signal,
  });
  expectedStatus(response.status, 204, 'endpoint deletion');
}

export async function listEndpointKeys(
  endpointId: string,
  cursor?: string,
  signal?: AbortSignal,
): Promise<Page<EndpointKey>> {
  const response = await coreRequest(
    `/api/endpoints/${pathID(endpointId, 'endpoint id')}/keys?${pageQuery(cursor)}`,
    { signal },
  );
  expectedStatus(response.status, 200, 'endpoint key list');
  const page = normalizeEndpointKeyPage(response.payload);
  if (page.data.some((key) => key.endpoint_id !== endpointId)) {
    throw new ApiError('invalid_response', 'The server returned a key for another endpoint.', 200);
  }
  return page;
}

export async function createEndpointKey(
  endpointId: string,
  input: EndpointKeyCreateInput,
  operation: OperationIdentity,
  signal?: AbortSignal,
): Promise<EndpointKey> {
  const record = exactInput(
    input,
    ['secret', 'note', 'enabled', 'force_store_false', 'ownership_confirmed'],
    [],
    'endpoint key creation input',
  );
  if (record.ownership_confirmed !== true) {
    throw new ApiError('invalid_request', 'Endpoint key ownership must be confirmed.', 400);
  }
  const payload: EndpointKeyCreateInput = {
    secret: validateEndpointSecret(record.secret as string),
    note: validateScalarInput(record.note, 1_024, 'endpoint key note'),
    enabled: exactBooleanInput(record.enabled, 'endpoint key enabled state'),
    force_store_false: exactBooleanInput(record.force_store_false, 'endpoint key store policy'),
    ownership_confirmed: true,
  };
  const response = await coreRequest(`/api/endpoints/${pathID(endpointId, 'endpoint id')}/keys`, {
    method: 'POST',
    headers: operationHeaders(operation),
    json: payload,
    signal,
  });
  expectedStatus(response.status, 201, 'endpoint key creation');
  const key = normalizeEndpointKey(response.payload);
  if (key.endpoint_id !== endpointId)
    throw new ApiError('invalid_response', 'The server returned a key for another endpoint.', 200);
  return key;
}

export async function patchEndpointKey(
  endpointId: string,
  keyId: string,
  input: EndpointKeyPatchInput,
  operation: OperationIdentity,
  signal?: AbortSignal,
): Promise<EndpointKey> {
  const record = exactInput(
    input,
    ['expected_revision'],
    ['note', 'enabled', 'force_store_false'],
    'endpoint key update input',
  );
  if (!['note', 'enabled', 'force_store_false'].some((key) => Object.hasOwn(record, key))) {
    throw new ApiError('invalid_request', 'Invalid endpoint key update input.', 400);
  }
  const payload: EndpointKeyPatchInput = {
    expected_revision: revisionField(record.expected_revision, 'endpoint key revision', true),
    ...(Object.hasOwn(record, 'note')
      ? { note: validateScalarInput(record.note, 1_024, 'endpoint key note') }
      : {}),
    ...(Object.hasOwn(record, 'enabled')
      ? { enabled: exactBooleanInput(record.enabled, 'endpoint key enabled state') }
      : {}),
    ...(Object.hasOwn(record, 'force_store_false')
      ? {
          force_store_false: exactBooleanInput(
            record.force_store_false,
            'endpoint key store policy',
          ),
        }
      : {}),
  };
  const response = await coreRequest(
    `/api/endpoints/${pathID(endpointId, 'endpoint id')}/keys/${pathID(keyId, 'endpoint key id')}`,
    { method: 'PATCH', headers: operationHeaders(operation), json: payload, signal },
  );
  expectedStatus(response.status, 200, 'endpoint key update');
  const key = normalizeEndpointKey(response.payload);
  if (key.id !== keyId || key.endpoint_id !== endpointId) {
    throw new ApiError('invalid_response', 'The server returned a different endpoint key.', 200);
  }
  return key;
}

export async function deleteEndpointKey(
  endpointId: string,
  keyId: string,
  expectedRevision: string,
  operation: OperationIdentity,
  signal?: AbortSignal,
): Promise<void> {
  validateRevisionInput(expectedRevision, 'endpoint key revision', true);
  const response = await coreRequest(
    `/api/endpoints/${pathID(endpointId, 'endpoint id')}/keys/${pathID(keyId, 'endpoint key id')}`,
    {
      method: 'DELETE',
      headers: operationHeaders(operation),
      json: { expected_revision: expectedRevision },
      signal,
    },
  );
  expectedStatus(response.status, 204, 'endpoint key deletion');
}

export async function getCatalog(
  endpointId: string,
  keyId: string,
  cursor?: string,
  signal?: AbortSignal,
  limit = 50,
): Promise<CatalogView> {
  const response = await coreRequest(
    `/api/endpoints/${pathID(endpointId, 'endpoint id')}/keys/${pathID(keyId, 'endpoint key id')}/models?${pageQuery(cursor, limit)}`,
    { signal },
  );
  expectedStatus(response.status, 200, 'catalog');
  const catalog = normalizeCatalogView(response.payload);
  if (catalog.automatic_entries.length + catalog.manual_entries.length > limit) {
    throw new ApiError('invalid_response', 'The server returned an oversized catalog page.', 200);
  }
  return catalog;
}

export async function refreshDiscovery(
  endpointId: string,
  keyId: string,
  operation: OperationIdentity,
  signal?: AbortSignal,
): Promise<DiscoveryAccepted> {
  const response = await coreRequest(
    `/api/endpoints/${pathID(endpointId, 'endpoint id')}/keys/${pathID(keyId, 'endpoint key id')}/models/refresh`,
    { method: 'POST', headers: operationHeaders(operation), signal },
  );
  expectedStatus(response.status, 202, 'discovery request');
  const accepted = normalizeDiscoveryAccepted(response.payload);
  if (accepted.evidence.state !== 'checking') {
    throw new ApiError(
      'invalid_response',
      'The server returned invalid discovery acceptance evidence.',
      200,
    );
  }
  return accepted;
}

export async function createManualEntries(
  endpointId: string,
  keyId: string,
  entries: readonly { upstream_model_id: string; provider: string }[],
  operation: OperationIdentity,
  signal?: AbortSignal,
): Promise<ManualEntriesResponse> {
  if (!Array.isArray(entries) || entries.length < 1 || entries.length > 100) {
    throw new ApiError('invalid_request', 'Invalid manual catalog entry count.', 400);
  }
  const canonical = entries.map((entry) => {
    const record = exactInput(entry, ['upstream_model_id', 'provider'], [], 'manual catalog entry');
    return {
      upstream_model_id: validateManualValue(record.upstream_model_id as string, 512, false),
      provider: validateManualValue(record.provider as string, 128, true),
    };
  });
  const response = await coreRequest(
    `/api/endpoints/${pathID(endpointId, 'endpoint id')}/keys/${pathID(keyId, 'endpoint key id')}/models/manual`,
    { method: 'POST', headers: operationHeaders(operation), json: { entries: canonical }, signal },
  );
  expectedStatus(response.status, 201, 'manual catalog creation');
  const result = normalizeManualEntriesResponse(response.payload);
  const expectedPairs = canonical
    .map((entry) => `${entry.upstream_model_id}\u0000${entry.provider}`)
    .sort();
  const actualPairs = result.entries
    .map((entry) => `${entry.upstream_model_id}\u0000${entry.provider}`)
    .sort();
  if (
    result.entries.length !== canonical.length ||
    actualPairs.some((pair, index) => pair !== expectedPairs[index])
  ) {
    throw new ApiError(
      'invalid_response',
      'The server returned a different manual catalog batch.',
      200,
    );
  }
  return result;
}

export async function updateManualEntry(
  endpointId: string,
  keyId: string,
  entryId: string,
  input: {
    upstream_model_id: string;
    provider: string;
    expected_pair_revision: string;
    replacements: BindingReplacement[];
  },
  operation: OperationIdentity,
  signal?: AbortSignal,
): Promise<ManualUpdateResponse> {
  const record = exactInput(
    input,
    ['upstream_model_id', 'provider', 'expected_pair_revision', 'replacements'],
    [],
    'manual catalog update input',
  );
  if (!Array.isArray(record.replacements) || record.replacements.length > 256) {
    throw new ApiError('invalid_request', 'Invalid binding replacements.', 400);
  }
  const replacementIds = new Set<string>();
  const canonicalReplacements = record.replacements.map((replacement) => {
    const fields = exactInput(
      replacement,
      ['binding_id', 'replacement_upstream_model_id'],
      [],
      'binding replacement',
    );
    const bindingId = validateResourceId(fields.binding_id as string, 'binding id');
    const replacementModel = validateManualValue(
      fields.replacement_upstream_model_id as string,
      512,
      false,
    );
    if (replacementIds.has(bindingId)) {
      throw new ApiError('invalid_request', 'Duplicate binding replacement.', 400);
    }
    replacementIds.add(bindingId);
    return { binding_id: bindingId, replacement_upstream_model_id: replacementModel };
  });
  const payload = {
    upstream_model_id: validateManualValue(record.upstream_model_id as string, 512, false),
    provider: validateManualValue(record.provider as string, 128, true),
    expected_pair_revision: revisionField(
      record.expected_pair_revision,
      'catalog pair revision',
      true,
    ),
    replacements: canonicalReplacements,
  };
  const response = await coreRequest(
    `/api/endpoints/${pathID(endpointId, 'endpoint id')}/keys/${pathID(keyId, 'endpoint key id')}/models/manual/${pathID(entryId, 'catalog entry id')}`,
    {
      method: 'PATCH',
      headers: operationHeaders(operation),
      json: payload,
      signal,
    },
  );
  expectedStatus(response.status, 200, 'manual catalog update');
  const result = normalizeManualUpdateResponse(response.payload);
  const entry = result.entries[0];
  if (
    !entry ||
    entry.id !== entryId ||
    entry.upstream_model_id !== payload.upstream_model_id ||
    entry.provider !== payload.provider ||
    BigInt(entry.pair_revision) <= BigInt(payload.expected_pair_revision)
  ) {
    throw new ApiError(
      'invalid_response',
      'The server returned a different manual catalog entry.',
      200,
    );
  }
  const projectedBindings = new Map<string, string>();
  for (const affected of result.affected_models) {
    for (const binding of affected.bindings) {
      if (projectedBindings.has(binding.id)) {
        throw new ApiError('invalid_response', 'The server repeated an affected binding.', 200);
      }
      projectedBindings.set(binding.id, binding.upstream_model_id);
    }
  }
  for (const replacement of canonicalReplacements) {
    if (
      projectedBindings.get(replacement.binding_id) !== replacement.replacement_upstream_model_id
    ) {
      throw new ApiError(
        'invalid_response',
        'The server omitted a required model connection replacement.',
        200,
      );
    }
  }
  return result;
}

export async function deleteManualEntry(
  endpointId: string,
  keyId: string,
  entryId: string,
  expectedPairRevision: string,
  replacements: BindingReplacement[],
  operation: OperationIdentity,
  signal?: AbortSignal,
): Promise<void> {
  validateRevisionInput(expectedPairRevision, 'catalog pair revision', true);
  if (!Array.isArray(replacements) || replacements.length > 256) {
    throw new ApiError('invalid_request', 'Invalid binding replacements.', 400);
  }
  const replacementIds = new Set<string>();
  const canonicalReplacements = replacements.map((replacement) => {
    const fields = exactInput(
      replacement,
      ['binding_id', 'replacement_upstream_model_id'],
      [],
      'binding replacement',
    );
    const bindingId = validateResourceId(fields.binding_id as string, 'binding id');
    const replacementModel = validateManualValue(
      fields.replacement_upstream_model_id as string,
      512,
      false,
    );
    if (replacementIds.has(bindingId)) {
      throw new ApiError('invalid_request', 'Duplicate binding replacement.', 400);
    }
    replacementIds.add(bindingId);
    return { binding_id: bindingId, replacement_upstream_model_id: replacementModel };
  });
  const response = await coreRequest(
    `/api/endpoints/${pathID(endpointId, 'endpoint id')}/keys/${pathID(keyId, 'endpoint key id')}/models/manual/${pathID(entryId, 'catalog entry id')}`,
    {
      method: 'DELETE',
      headers: operationHeaders(operation),
      json: { expected_pair_revision: expectedPairRevision, replacements: canonicalReplacements },
      signal,
    },
  );
  expectedStatus(response.status, 204, 'manual catalog deletion');
}

export async function listModels(
  cursor?: string,
  signal?: AbortSignal,
  limit = 50,
): Promise<Page<Model>> {
  const response = await coreRequest(`/api/models?${pageQuery(cursor, limit)}`, { signal });
  expectedStatus(response.status, 200, 'logical model list');
  const page = normalizeModelPage(response.payload);
  if (page.data.length > limit) {
    throw new ApiError(
      'invalid_response',
      'The server returned an oversized logical model page.',
      200,
    );
  }
  return page;
}

export async function getModel(modelId: string, signal?: AbortSignal): Promise<Model> {
  const response = await coreRequest(`/api/models/${pathID(modelId, 'model id')}`, { signal });
  expectedStatus(response.status, 200, 'logical model');
  const model = normalizeModel(response.payload);
  if (model.id !== modelId)
    throw new ApiError('invalid_response', 'The server returned a different logical model.', 200);
  return model;
}

export async function createModel(
  input: ModelCreateInput,
  operation: OperationIdentity,
  signal?: AbortSignal,
): Promise<Model> {
  const record = exactInput(
    input,
    ['provider', 'model'],
    ['route_strategy', 'silent_retry', 'flatten_tool_calls'],
    'logical model creation input',
  );
  const routeStrategy = record.route_strategy;
  if (routeStrategy !== undefined && routeStrategy !== 'ordered' && routeStrategy !== 'random') {
    throw new ApiError('invalid_request', 'Invalid route strategy.', 400);
  }
  const payload: ModelCreateInput = {
    provider: validatePersonalProviderName(record.provider as string),
    model: validateLogicalName(record.model as string),
    ...(routeStrategy === undefined ? {} : { route_strategy: routeStrategy }),
    ...(Object.hasOwn(record, 'silent_retry')
      ? { silent_retry: exactBooleanInput(record.silent_retry, 'silent retry setting') }
      : {}),
    ...(Object.hasOwn(record, 'flatten_tool_calls')
      ? { flatten_tool_calls: exactBooleanInput(record.flatten_tool_calls, 'tool call setting') }
      : {}),
  };
  const response = await coreRequest('/api/models', {
    method: 'POST',
    headers: operationHeaders(operation),
    json: payload,
    signal,
  });
  expectedStatus(response.status, 201, 'logical model creation');
  return normalizeModel(response.payload);
}

export async function patchModel(
  modelId: string,
  input: ModelPatchInput,
  operation: OperationIdentity,
  signal?: AbortSignal,
): Promise<Model> {
  const record = exactInput(
    input,
    ['expected_revision'],
    ['provider', 'model', 'route_strategy', 'silent_retry', 'flatten_tool_calls'],
    'logical model update input',
  );
  if (
    !['provider', 'model', 'route_strategy', 'silent_retry', 'flatten_tool_calls'].some((key) =>
      Object.hasOwn(record, key),
    )
  ) {
    throw new ApiError('invalid_request', 'Invalid logical model update input.', 400);
  }
  if (
    record.route_strategy !== undefined &&
    record.route_strategy !== 'ordered' &&
    record.route_strategy !== 'random'
  ) {
    throw new ApiError('invalid_request', 'Invalid route strategy.', 400);
  }
  const payload: ModelPatchInput = {
    expected_revision: revisionField(record.expected_revision, 'model revision', true),
    ...(Object.hasOwn(record, 'provider')
      ? { provider: validatePersonalProviderName(record.provider as string) }
      : {}),
    ...(Object.hasOwn(record, 'model')
      ? { model: validateLogicalName(record.model as string) }
      : {}),
    ...(Object.hasOwn(record, 'route_strategy')
      ? { route_strategy: record.route_strategy as ModelPatchInput['route_strategy'] }
      : {}),
    ...(Object.hasOwn(record, 'silent_retry')
      ? { silent_retry: exactBooleanInput(record.silent_retry, 'silent retry setting') }
      : {}),
    ...(Object.hasOwn(record, 'flatten_tool_calls')
      ? { flatten_tool_calls: exactBooleanInput(record.flatten_tool_calls, 'tool call setting') }
      : {}),
  };
  const response = await coreRequest(`/api/models/${pathID(modelId, 'model id')}`, {
    method: 'PATCH',
    headers: operationHeaders(operation),
    json: payload,
    signal,
  });
  expectedStatus(response.status, 200, 'logical model update');
  const model = normalizeModel(response.payload);
  if (model.id !== modelId)
    throw new ApiError('invalid_response', 'The server returned a different logical model.', 200);
  return model;
}

export async function deleteModel(
  modelId: string,
  expectedRevision: string,
  operation: OperationIdentity,
  signal?: AbortSignal,
): Promise<void> {
  validateRevisionInput(expectedRevision, 'model revision', true);
  const response = await coreRequest(`/api/models/${pathID(modelId, 'model id')}`, {
    method: 'DELETE',
    headers: operationHeaders(operation),
    json: { expected_revision: expectedRevision },
    signal,
  });
  expectedStatus(response.status, 204, 'logical model deletion');
}

export async function getBindingCandidates(
  modelId: string,
  filters: CandidateFilters,
  signal?: AbortSignal,
): Promise<Page<BindingCandidate>> {
  const canonical = canonicalCandidateFilters(filters);
  const params = new URLSearchParams();
  if (canonical.endpointId) params.set('endpoint_id', canonical.endpointId);
  if (canonical.keyId) params.set('key_id', canonical.keyId);
  if (canonical.source) params.set('source', canonical.source);
  if (canonical.query) params.set('q', canonical.query);
  if (canonical.cursor) params.set('cursor', canonical.cursor);
  params.set('limit', String(canonical.limit));
  const response = await coreRequest(
    `/api/models/${pathID(modelId, 'model id')}/binding-candidates?${params}`,
    { signal },
  );
  expectedStatus(response.status, 200, 'binding candidates');
  const page = normalizeBindingCandidatePage(response.payload);
  if (page.data.length > canonical.limit) {
    throw new ApiError(
      'invalid_response',
      'The server returned an oversized binding candidate page.',
      200,
    );
  }
  if (
    canonical.keyId &&
    page.data.some((candidate) => candidate.endpoint_key_id !== canonical.keyId)
  ) {
    throw new ApiError(
      'invalid_response',
      'The server returned a candidate for another endpoint key.',
      200,
    );
  }
  return page;
}

export async function getBindings(
  modelId: string,
  signal?: AbortSignal,
): Promise<BindingsResponse> {
  const response = await coreRequest(`/api/models/${pathID(modelId, 'model id')}/bindings`, {
    signal,
  });
  expectedStatus(response.status, 200, 'bindings');
  return normalizeBindingsResponse(response.payload);
}

export async function addBindings(
  modelId: string,
  expectedBindingRevision: string,
  selections: BindingSelection[],
  operation: OperationIdentity,
  signal?: AbortSignal,
): Promise<BindingsResponse> {
  validateRevisionInput(expectedBindingRevision, 'binding revision');
  if (!Array.isArray(selections) || selections.length < 1 || selections.length > 256) {
    throw new ApiError('invalid_request', 'Invalid binding selection count.', 400);
  }
  const selectionKeys = new Set<string>();
  const canonicalSelections = selections.map((selection) => {
    const record = exactInput(
      selection,
      ['endpoint_key_id', 'upstream_model_id'],
      [],
      'binding selection',
    );
    const endpointKeyId = validateResourceId(record.endpoint_key_id as string, 'endpoint key id');
    const upstreamModelId = validateManualValue(record.upstream_model_id as string, 512, false);
    const key = `${endpointKeyId}\u0000${upstreamModelId}`;
    if (selectionKeys.has(key))
      throw new ApiError('invalid_request', 'Duplicate binding selection.', 400);
    selectionKeys.add(key);
    return { endpoint_key_id: endpointKeyId, upstream_model_id: upstreamModelId };
  });
  const response = await coreRequest(`/api/models/${pathID(modelId, 'model id')}/bindings/batch`, {
    method: 'POST',
    headers: operationHeaders(operation),
    json: { expected_binding_revision: expectedBindingRevision, selections: canonicalSelections },
    signal,
  });
  expectedStatus(response.status, 201, 'binding creation');
  return normalizeBindingsResponse(response.payload);
}

export async function orderBindings(
  modelId: string,
  expectedBindingRevision: string,
  order: string[],
  operation: OperationIdentity,
  signal?: AbortSignal,
): Promise<BindingsResponse> {
  validateRevisionInput(expectedBindingRevision, 'binding revision');
  if (!Array.isArray(order) || order.length > 256 || new Set(order).size !== order.length) {
    throw new ApiError('invalid_request', 'Invalid binding order.', 400);
  }
  const canonicalOrder = order.map((bindingId) => validateResourceId(bindingId, 'binding id'));
  const response = await coreRequest(`/api/models/${pathID(modelId, 'model id')}/bindings/order`, {
    method: 'PUT',
    headers: operationHeaders(operation),
    json: { expected_binding_revision: expectedBindingRevision, order: canonicalOrder },
    signal,
  });
  expectedStatus(response.status, 200, 'binding order update');
  const result = normalizeBindingsResponse(response.payload);
  if (
    result.bindings.length !== canonicalOrder.length ||
    result.bindings.some((binding, index) => binding.id !== canonicalOrder[index])
  ) {
    throw new ApiError('invalid_response', 'The server returned a different binding order.', 200);
  }
  return result;
}

export async function deleteBinding(
  modelId: string,
  bindingId: string,
  expectedBindingRevision: string,
  operation: OperationIdentity,
  signal?: AbortSignal,
): Promise<BindingsResponse> {
  validateRevisionInput(expectedBindingRevision, 'binding revision');
  const response = await coreRequest(
    `/api/models/${pathID(modelId, 'model id')}/bindings/${pathID(bindingId, 'binding id')}`,
    {
      method: 'DELETE',
      headers: operationHeaders(operation),
      json: { expected_binding_revision: expectedBindingRevision },
      signal,
    },
  );
  expectedStatus(response.status, 200, 'binding deletion');
  const result = normalizeBindingsResponse(response.payload);
  if (result.bindings.some((binding) => binding.id === bindingId)) {
    throw new ApiError(
      'invalid_response',
      'The deleted binding remained in the server response.',
      200,
    );
  }
  return result;
}

export async function getCallerKey(signal?: AbortSignal): Promise<CallerKeyAuthority> {
  const response = await coreRequest('/api/caller-key', { signal });
  expectedStatus(response.status, 200, 'CallerKey');
  return normalizeCallerKeyAuthority(
    response.payload,
    response.headers.get('X-Nonbiri-CallerKey-Generation'),
  );
}

export async function regenerateCallerKey(
  expectedGeneration: string,
  signal?: AbortSignal,
): Promise<CallerKeySecret> {
  validateRevisionInput(expectedGeneration, 'CallerKey generation');
  const response = await coreRequest('/api/caller-key/regenerate', {
    method: 'POST',
    json: { expected_generation: expectedGeneration },
    signal,
  });
  expectedStatus(response.status, 200, 'CallerKey generation');
  return normalizeCallerKeySecret(response.payload, expectedGeneration);
}

export async function collectAllModels(signal?: AbortSignal): Promise<Model[]> {
  const models: Model[] = [];
  const modelIds = new Set<string>();
  const cursors = new Set<string>();
  let cursor: string | undefined;
  for (let pageNumber = 0; pageNumber < 100; pageNumber += 1) {
    const page = await listModels(cursor, signal, 100);
    for (const model of page.data) {
      if (modelIds.has(model.id))
        throw new ApiError('invalid_response', 'The model list repeated an item.', 200);
      modelIds.add(model.id);
      models.push(model);
    }
    if (!page.next_cursor) return models;
    if (cursors.has(page.next_cursor))
      throw new ApiError('invalid_response', 'The model list repeated a cursor.', 200);
    cursors.add(page.next_cursor);
    cursor = page.next_cursor;
  }
  throw new ApiError('invalid_response', 'The model list exceeded the supported page bound.', 200);
}
