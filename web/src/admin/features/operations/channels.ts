import { ApiError, apiFetch } from '@shared/query/http';
import { decoded, idempotentOptions, queryPath } from '@shared/operations/api';
import {
  boolean,
  cursor,
  decimal,
  invalidResponse,
  nullableUnixSecond,
  opaqueID,
  oneOf,
  page,
  record,
  string,
  unixSecond,
  type CursorPage,
} from '@shared/operations/wire';

export const MAINSTREAM_CHANNEL_CATEGORIES = ['subscription', 'api_platform'] as const;
export type MainstreamChannelCategory = (typeof MAINSTREAM_CHANNEL_CATEGORIES)[number];

export const MAINSTREAM_CHANNEL_STATES = ['active', 'retired'] as const;
export type MainstreamChannelState = (typeof MAINSTREAM_CHANNEL_STATES)[number];
export type MainstreamChannelListState = MainstreamChannelState | 'all';

export const MAINSTREAM_CONNECTOR_TYPES = [
  'openai-compatible',
  'anthropic-compatible',
] as const;
export type MainstreamConnectorType = (typeof MAINSTREAM_CONNECTOR_TYPES)[number];

export interface AdminMainstreamChannel {
  id: string;
  name: string;
  category: MainstreamChannelCategory;
  connector_type: MainstreamConnectorType;
  base_url: string;
  enabled: boolean;
  state: MainstreamChannelState;
  revision: string;
  created_at: number;
  updated_at: number;
  retired_at: number | null;
}

export interface AdminMainstreamChannelCreate {
  name: string;
  category: MainstreamChannelCategory;
  connector_type: MainstreamConnectorType;
  base_url: string;
  enabled: boolean;
}

export interface AdminMainstreamChannelPatch {
  name?: string;
  category?: MainstreamChannelCategory;
  connector_type?: MainstreamConnectorType;
  base_url?: string;
  enabled?: boolean;
  expected_revision: string;
}

const CHANNEL_FIELDS = [
  'id',
  'name',
  'category',
  'connector_type',
  'base_url',
  'enabled',
  'state',
  'revision',
  'created_at',
  'updated_at',
  'retired_at',
] as const;

function hasForbiddenControl(value: string): boolean {
  return Array.from(value).some((character) => {
    const point = character.codePointAt(0) ?? 0;
    return point < 0x20 || (point >= 0x7f && point <= 0x9f);
  });
}

function normalizedChannelName(value: unknown): string {
  const name = string(value, 'mainstream channel name', {
    min: 1,
    max: 128,
  });
  // `trim` is only a validation check. The authority's exact Unicode scalar
  // sequence is returned unchanged and is never normalized client-side.
  if (name.trim() !== name || hasForbiddenControl(name)) invalidResponse('mainstream channel name');
  return name;
}

function normalizedChannelURL(value: unknown): string {
  const url = string(value, 'mainstream channel canonical URL', {
    min: 1,
    max: 4_096,
    bytes: 4_096,
  });
  if (hasForbiddenControl(url)) invalidResponse('mainstream channel canonical URL');
  return url;
}

export function normalizeAdminMainstreamChannel(value: unknown): AdminMainstreamChannel {
  const root = record(value, CHANNEL_FIELDS, 'administrator mainstream channel');
  const state = oneOf(root.state, MAINSTREAM_CHANNEL_STATES, 'mainstream channel state');
  const enabled = boolean(root.enabled, 'mainstream channel enabled state');
  const retiredAt = nullableUnixSecond(root.retired_at, 'mainstream channel retirement time');
  if (state === 'active' && retiredAt !== null) invalidResponse('active mainstream channel retirement state');
  if (state === 'retired' && (retiredAt === null || enabled)) {
    invalidResponse('retired mainstream channel state');
  }
  return {
    id: opaqueID(root.id, 'mch_', 'mainstream channel id'),
    name: normalizedChannelName(root.name),
    category: oneOf(root.category, MAINSTREAM_CHANNEL_CATEGORIES, 'mainstream channel category'),
    connector_type: oneOf(
      root.connector_type,
      MAINSTREAM_CONNECTOR_TYPES,
      'mainstream channel connector',
    ),
    base_url: normalizedChannelURL(root.base_url),
    enabled,
    state,
    revision: decimal(root.revision, 'mainstream channel revision', { positive: true }),
    created_at: unixSecond(root.created_at, 'mainstream channel creation time'),
    updated_at: unixSecond(root.updated_at, 'mainstream channel update time'),
    retired_at: retiredAt,
  };
}

export const adminMainstreamChannelKeys = {
  root: ['admin', 'operations', 'mainstream-channels'] as const,
  list: (state: MainstreamChannelListState, cursorValue: string | null) =>
    ['admin', 'operations', 'mainstream-channels', 'list', state, cursorValue] as const,
  detail: (id: string) => ['admin', 'operations', 'mainstream-channels', 'detail', id] as const,
};

function requestError(message: string): never {
  throw new ApiError('invalid_request', message, 400);
}

function requestText(value: unknown, label: string, maximum: number): string {
  if (typeof value !== 'string' || value.length === 0 || Array.from(value).length > maximum) {
    return requestError(`Invalid ${label}.`);
  }
  if (new TextEncoder().encode(value).byteLength > 4_096) return requestError(`Invalid ${label}.`);
  for (const character of value) {
    const point = character.codePointAt(0) ?? 0;
    if (point < 0x20 || (point >= 0x7f && point <= 0x9f)) return requestError(`Invalid ${label}.`);
  }
  return value;
}

function requestName(value: unknown): string {
  const name = requestText(value, 'mainstream channel name', 128);
  if (name.trim() !== name) return requestError('Invalid mainstream channel name.');
  return name;
}

function requestBaseURL(value: unknown): string {
  return requestText(value, 'mainstream channel URL', 4_096);
}

function requestCategory(value: unknown): MainstreamChannelCategory {
  if (!MAINSTREAM_CHANNEL_CATEGORIES.includes(value as MainstreamChannelCategory)) {
    return requestError('Invalid mainstream channel category.');
  }
  return value as MainstreamChannelCategory;
}

function requestConnector(value: unknown): MainstreamConnectorType {
  if (!MAINSTREAM_CONNECTOR_TYPES.includes(value as MainstreamConnectorType)) {
    return requestError('Invalid mainstream channel connector.');
  }
  return value as MainstreamConnectorType;
}

function requestEnabled(value: unknown): boolean {
  if (typeof value !== 'boolean') return requestError('Invalid mainstream channel enabled state.');
  return value;
}

function requestRevision(value: unknown): string {
  if (typeof value !== 'string' || !/^(?:[1-9][0-9]*)$/.test(value)) {
    return requestError('Invalid mainstream channel revision.');
  }
  return value;
}

function requestListState(value: unknown): MainstreamChannelListState {
  if (value !== 'active' && value !== 'retired' && value !== 'all') {
    return requestError('Invalid mainstream channel list state.');
  }
  return value;
}

function requestLimit(value: unknown): number {
  if (!Number.isSafeInteger(value) || (value as number) < 1 || (value as number) > 100) {
    return requestError('Invalid mainstream channel page size.');
  }
  return value as number;
}

export function getAdminMainstreamChannels(
  state: MainstreamChannelListState = 'active',
  cursorValue: string | null = null,
  limit = 50,
  signal?: AbortSignal,
): Promise<CursorPage<AdminMainstreamChannel>> {
  const requestedState = requestListState(state);
  const requestedLimit = requestLimit(limit);
  if (cursorValue !== null) cursor(cursorValue, 'mainstream channel cursor');
  return decoded(
    queryPath('/admin/api/mainstream-channels', {
      state: requestedState,
      cursor: cursorValue,
      limit: requestedLimit,
    }),
    (value) => {
      const result = page(value, 'mainstream channel page', normalizeAdminMainstreamChannel);
      if (result.data.length > 100) invalidResponse('mainstream channel page size');
      return result;
    },
    { signal },
  );
}

export function getAdminMainstreamChannel(
  id: string,
  signal?: AbortSignal,
): Promise<AdminMainstreamChannel> {
  const channelID = opaqueID(id, 'mch_', 'mainstream channel id');
  return decoded(
    `/admin/api/mainstream-channels/${encodeURIComponent(channelID)}`,
    normalizeAdminMainstreamChannel,
    { signal },
  );
}

function createBody(input: AdminMainstreamChannelCreate): AdminMainstreamChannelCreate {
  if (input === null || typeof input !== 'object') return requestError('Invalid mainstream channel.');
  return {
    name: requestName(input.name),
    category: requestCategory(input.category),
    connector_type: requestConnector(input.connector_type),
    base_url: requestBaseURL(input.base_url),
    enabled: requestEnabled(input.enabled),
  };
}

export function createAdminMainstreamChannel(
  input: AdminMainstreamChannelCreate,
  idempotencyKey: string,
): Promise<AdminMainstreamChannel> {
  return decoded(
    '/admin/api/mainstream-channels',
    normalizeAdminMainstreamChannel,
    idempotentOptions(idempotencyKey, { method: 'POST', json: createBody(input) }),
  );
}

function patchBody(input: AdminMainstreamChannelPatch): AdminMainstreamChannelPatch {
  if (input === null || typeof input !== 'object') return requestError('Invalid mainstream channel patch.');
  const body: AdminMainstreamChannelPatch = {
    expected_revision: requestRevision(input.expected_revision),
  };
  let changed = false;
  if (Object.hasOwn(input, 'name')) {
    body.name = requestName(input.name);
    changed = true;
  }
  if (Object.hasOwn(input, 'category')) {
    body.category = requestCategory(input.category);
    changed = true;
  }
  if (Object.hasOwn(input, 'connector_type')) {
    body.connector_type = requestConnector(input.connector_type);
    changed = true;
  }
  if (Object.hasOwn(input, 'base_url')) {
    body.base_url = requestBaseURL(input.base_url);
    changed = true;
  }
  if (Object.hasOwn(input, 'enabled')) {
    body.enabled = requestEnabled(input.enabled);
    changed = true;
  }
  if (!changed) return requestError('A mainstream channel patch must change a field.');
  return body;
}

export function patchAdminMainstreamChannel(
  id: string,
  input: AdminMainstreamChannelPatch,
  idempotencyKey: string,
): Promise<AdminMainstreamChannel> {
  const channelID = opaqueID(id, 'mch_', 'mainstream channel id');
  return decoded(
    `/admin/api/mainstream-channels/${encodeURIComponent(channelID)}`,
    normalizeAdminMainstreamChannel,
    idempotentOptions(idempotencyKey, { method: 'PATCH', json: patchBody(input) }),
  );
}

export async function retireAdminMainstreamChannel(
  id: string,
  expectedRevision: string,
  idempotencyKey: string,
): Promise<void> {
  const channelID = opaqueID(id, 'mch_', 'mainstream channel id');
  await apiFetch<void>(
    `/admin/api/mainstream-channels/${encodeURIComponent(channelID)}`,
    idempotentOptions(idempotencyKey, {
      method: 'DELETE',
      json: {
        expected_revision: requestRevision(expectedRevision),
        confirmation: 'retire',
      },
    }),
  );
}
