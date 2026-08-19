import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ApiError, apiFetch } from '@shared/query/http';
import {
  asRecord,
  booleanValue,
  dateValue,
  idValue,
  hasControlCharacters,
  integerValue,
  isListPayload,
  listResult,
  optionalText,
  text,
  type UnknownRecord,
} from '@shared/query/normalize';

export interface UserSummary {
  id: string;
  username: string;
  avatar?: string;
  avatar_url?: string;
  guild_nick: string;
  guild_avatar_url?: string;
  lang: 'zh' | 'en';
  is_banned: boolean;
  blocked_reason?: string;
  endpoint_limit?: number;
  rpm_limit?: number;
  created_at: string;
}

export interface UserSessionResponse {
  user: UserSummary;
}

export interface UsageSummary {
  total_requests: number;
  total_prompt_tokens: number;
  total_completion_tokens: number;
  total_unknown_usage_requests: number;
}

export interface Endpoint {
  id: string;
  connector_type: string;
  base_url: string;
  note: string;
  enabled: boolean;
  model_fetch_failed: boolean;
  model_fetch_failed_at: string;
  created_at: string;
  updated_at: string;
}

export interface EndpointKey {
  id: string;
  /** Head/tail fragment only. The full secret is never part of this type. */
  display?: string;
  note: string;
  enabled: boolean;
  created_at: string;
  updated_at: string;
}

export interface UpstreamModel {
  upstream_model_id: string;
  provider: string;
  fetched_at: string;
  status: string;
}

export interface PlatformModel {
  id: string;
  provider: string;
  model: string;
  full_name: string;
  route_strategy: 'ordered' | 'random';
  silent_retry: boolean;
  binding_count: number;
  created_at: string;
  updated_at: string;
}

export interface ModelBinding {
  id: string;
  endpoint_key_id: string;
  upstream_model_id: string;
  ord: number;
}

export interface CallerKeyMetadata {
  display: string;
  created_at: string;
  updated_at: string;
}

export interface CallerKeySecret {
  secret: string;
}

export interface UserIssue {
  id: string;
  kind: string;
  message: string;
  ref: string;
  created_at: string;
  resolved: boolean;
  resolved_at?: string;
}

export interface IssuePage {
  items: UserIssue[];
  hasMore: boolean;
  nextCursor?: string;
}

export const userKeys = {
  all: ['user'] as const,
  session: ['user', 'session'] as const,
  me: ['user', 'me'] as const,
  usage: ['user', 'usage'] as const,
  endpoints: ['user', 'endpoints'] as const,
  endpointKeysRoot: ['user', 'endpoint-keys'] as const,
  endpointKeys: (endpointId: string) => ['user', 'endpoint-keys', endpointId] as const,
  keyModelsRoot: ['user', 'key-models'] as const,
  keyModels: (endpointId: string, keyId: string) =>
    ['user', 'key-models', endpointId, keyId] as const,
  models: ['user', 'models'] as const,
  bindingsRoot: ['user', 'bindings'] as const,
  bindings: (modelId: string) => ['user', 'bindings', modelId] as const,
  callerKey: ['user', 'caller-key'] as const,
  issues: ['user', 'issues'] as const,
  issueResolve: (issueId: string) => ['user', 'issues', 'resolve', issueId] as const,
};

function recordValue(record: UnknownRecord, key: string): unknown {
  return record[key];
}

function normalizeUser(value: unknown): UserSummary {
  const record = asRecord(value) ?? {};
  const lang = recordValue(record, 'lang') === 'zh' ? 'zh' : 'en';
  const endpointLimit = recordValue(record, 'endpoint_limit');
  const rpmLimit = recordValue(record, 'rpm_limit');
  return {
    id: idValue(recordValue(record, 'id')),
    username: text(recordValue(record, 'username'), 128, '—'),
    ...(optionalText(recordValue(record, 'avatar'), 512)
      ? { avatar: optionalText(recordValue(record, 'avatar'), 512) }
      : {}),
    ...(optionalText(recordValue(record, 'avatar_url'), 512)
      ? { avatar_url: optionalText(recordValue(record, 'avatar_url'), 512) }
      : {}),
    guild_nick: text(recordValue(record, 'guild_nick'), 128),
    ...(optionalText(recordValue(record, 'guild_avatar_url'), 512)
      ? { guild_avatar_url: optionalText(recordValue(record, 'guild_avatar_url'), 512) }
      : {}),
    lang,
    is_banned: booleanValue(recordValue(record, 'is_banned')),
    ...(optionalText(recordValue(record, 'blocked_reason'), 512)
      ? { blocked_reason: optionalText(recordValue(record, 'blocked_reason'), 512) }
      : {}),
    ...(typeof endpointLimit === 'number' && Number.isFinite(endpointLimit)
      ? { endpoint_limit: Math.max(0, Math.trunc(endpointLimit)) }
      : {}),
    ...(typeof rpmLimit === 'number' && Number.isFinite(rpmLimit)
      ? { rpm_limit: Math.max(0, Math.trunc(rpmLimit)) }
      : {}),
    created_at: dateValue(recordValue(record, 'created_at')),
  };
}

function normalizeSession(value: unknown): UserSessionResponse {
  const record = asRecord(value);
  if (!record || !asRecord(record.user)) {
    throw new ApiError('invalid_response', 'The server returned an invalid session.', 200);
  }
  return { user: normalizeUser(record.user) };
}

function normalizeUsage(value: unknown): UsageSummary {
  const record = asRecord(value);
  if (!record) throw new ApiError('invalid_response', 'The server returned an invalid usage summary.', 200);
  const required = (key: string): number => {
    const raw = recordValue(record, key);
    if (typeof raw !== 'number' || !Number.isFinite(raw)) {
      throw new ApiError('invalid_response', 'The server returned an invalid usage summary.', 200);
    }
    return Math.max(0, Math.trunc(raw));
  };
  return {
    total_requests: required('total_requests'),
    total_prompt_tokens: required('total_prompt_tokens'),
    total_completion_tokens: required('total_completion_tokens'),
    total_unknown_usage_requests: required('total_unknown_usage_requests'),
  };
}

function listPayload(value: unknown): unknown[] {
  if (!isListPayload(value)) {
    throw new ApiError('invalid_response', 'The server returned an invalid list.', 200);
  }
  return listResult(value, 1000).items;
}

function normalizeEndpoint(value: unknown): Endpoint {
  const record = asRecord(value) ?? {};
  return {
    id: idValue(recordValue(record, 'id')),
    connector_type: text(recordValue(record, 'connector_type'), 96, 'openai-compatible'),
    base_url: text(recordValue(record, 'base_url'), 2048, '—'),
    note: text(recordValue(record, 'note'), 512),
    enabled: booleanValue(recordValue(record, 'enabled'), true),
    model_fetch_failed: booleanValue(recordValue(record, 'model_fetch_failed')),
    model_fetch_failed_at: dateValue(recordValue(record, 'model_fetch_failed_at')),
    created_at: dateValue(recordValue(record, 'created_at')),
    updated_at: dateValue(recordValue(record, 'updated_at')),
  };
}

function safeDisplayFragment(value: unknown, max = 64): string | undefined {
  const candidate = optionalText(value, max);
  return candidate && (candidate.includes('…') || candidate.includes('...')) ? candidate : undefined;
}

function fragmentFromRecord(record: UnknownRecord): string | undefined {
  const directDisplay = safeDisplayFragment(recordValue(record, 'display'));
  const head = optionalText(recordValue(record, 'display_head'), 4) ?? optionalText(recordValue(record, 'secret_head'), 4);
  const tail = optionalText(recordValue(record, 'display_tail'), 4) ?? optionalText(recordValue(record, 'secret_tail'), 4);
  if (directDisplay) return directDisplay;
  if (head && tail) return `${head}…${tail}`;
  if (head) return `${head}…`;
  if (tail) return `…${tail}`;
  return undefined;
}

function normalizeEndpointKey(value: unknown): EndpointKey {
  const record = asRecord(value) ?? {};
  const fragment = fragmentFromRecord(record);
  return {
    id: idValue(recordValue(record, 'id')),
    ...(fragment ? { display: fragment } : {}),
    note: text(recordValue(record, 'note'), 512),
    enabled: booleanValue(recordValue(record, 'enabled'), true),
    created_at: dateValue(recordValue(record, 'created_at')),
    updated_at: dateValue(recordValue(record, 'updated_at')),
  };
}

function normalizeUpstreamModel(value: unknown): UpstreamModel {
  const record = asRecord(value) ?? {};
  return {
    upstream_model_id: text(recordValue(record, 'upstream_model_id'), 256, '—'),
    provider: text(recordValue(record, 'provider'), 128, '—'),
    fetched_at: dateValue(recordValue(record, 'fetched_at')),
    status: text(recordValue(record, 'status'), 64, 'unknown'),
  };
}

function normalizePlatformModel(value: unknown): PlatformModel {
  const record = asRecord(value) ?? {};
  return {
    id: idValue(recordValue(record, 'id')),
    provider: text(recordValue(record, 'provider'), 64, '—'),
    model: text(recordValue(record, 'model'), 64, '—'),
    full_name: text(recordValue(record, 'full_name'), 160, '—'),
    route_strategy: recordValue(record, 'route_strategy') === 'random' ? 'random' : 'ordered',
    silent_retry: booleanValue(recordValue(record, 'silent_retry')),
    binding_count: Math.max(0, integerValue(recordValue(record, 'binding_count'))),
    created_at: dateValue(recordValue(record, 'created_at')),
    updated_at: dateValue(recordValue(record, 'updated_at')),
  };
}

function normalizeBinding(value: unknown): ModelBinding {
  const record = asRecord(value) ?? {};
  return {
    id: idValue(recordValue(record, 'id')),
    endpoint_key_id: idValue(recordValue(record, 'endpoint_key_id')),
    upstream_model_id: text(recordValue(record, 'upstream_model_id'), 256, '—'),
    ord: Math.max(0, integerValue(recordValue(record, 'ord'))),
  };
}

function normalizeCallerKey(value: unknown): CallerKeyMetadata {
  const record = asRecord(value) ?? {};
  return {
    display: fragmentFromRecord(record) ?? '—',
    created_at: dateValue(recordValue(record, 'created_at')),
    updated_at: dateValue(recordValue(record, 'updated_at')),
  };
}

function normalizeUserIssue(value: unknown): UserIssue {
  const record = asRecord(value) ?? {};
  return {
    id: idValue(recordValue(record, 'id')),
    kind: text(recordValue(record, 'kind'), 128, '—'),
    message: text(recordValue(record, 'message'), 1024),
    ref: text(recordValue(record, 'ref'), 256),
    created_at: dateValue(recordValue(record, 'created_at')),
    resolved: booleanValue(recordValue(record, 'resolved')),
    ...(optionalText(recordValue(record, 'resolved_at'), 64)
      ? { resolved_at: dateValue(recordValue(record, 'resolved_at')) }
      : {}),
  };
}

/**
 * Normalize one paginated issue page `{data, has_more}`. The browser never
 * invents issues: an invalid envelope throws so a broken server cannot fake
 * a local issue list.
 */
function normalizeIssuePage(value: unknown, pageSize = 20): IssuePage {
  if (!isListPayload(value)) {
    throw new ApiError('invalid_response', 'The server returned an invalid issue list.', 200);
  }
  const record = asRecord(value);
  const result = listResult(value, pageSize);
  const hasMore =
    typeof record?.has_more === 'boolean' ? record.has_more : result.hasNext;
  const items = result.items.map(normalizeUserIssue);
  const last = items[items.length - 1];
  return {
    items,
    hasMore,
    ...(hasMore && last && last.id !== '—' ? { nextCursor: last.id } : {}),
  };
}

/** Validate the one-time response without ever normalizing it into a query. */
export function normalizeSecret(value: unknown): CallerKeySecret {
  const record = asRecord(value);
  const rawSecret = record?.secret;
  if (typeof rawSecret !== 'string') {
    throw new ApiError('invalid_response', 'The server returned an invalid caller key.', 200);
  }
  const secret = rawSecret;
  if (secret !== secret.trim() || !secret.startsWith('nbk_') || secret.length < 8 || secret.length > 512) {
    throw new ApiError('invalid_response', 'The server returned an invalid caller key.', 200);
  }
  if (hasControlCharacters(secret)) {
    throw new ApiError('invalid_response', 'The server returned an invalid caller key.', 200);
  }
  return { secret };
}

export function useUserSession() {
  return useQuery({
    queryKey: userKeys.session,
    queryFn: async () => normalizeSession(await apiFetch<unknown>('/api/session')),
    staleTime: 15_000,
    retry: false,
  });
}

export function useUserMe(enabled = true) {
  return useQuery({
    queryKey: userKeys.me,
    queryFn: async () => {
      const payload = asRecord(await apiFetch<unknown>('/api/me'));
      return normalizeUser(payload?.user ?? payload);
    },
    enabled,
  });
}

/**
 * Session-only self-service profile update (lang only). Per-user RPM
 * limits are administrator-set; the browser never sends rpm_limit.
 * endpoint_limit / admin / ban fields are rejected by the server.
 */
export function useUpdateUserProfile() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (values: { lang?: 'zh' | 'en' }) =>
      apiFetch<unknown>('/api/me', { method: 'PATCH', json: values }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: userKeys.me });
    },
  });
}

export function useUserUsage(enabled = true) {
  return useQuery({
    queryKey: userKeys.usage,
    queryFn: async () => normalizeUsage(await apiFetch<unknown>('/api/me/usage')),
    enabled,
  });
}

export function useEndpoints(enabled = true) {
  return useQuery({
    queryKey: userKeys.endpoints,
    queryFn: async () => listPayload(await apiFetch<unknown>('/api/endpoints')).map(normalizeEndpoint),
    enabled,
  });
}

export function useEndpointKeys(endpointId: string | undefined, enabled = true) {
  return useQuery({
    queryKey: endpointId ? userKeys.endpointKeys(endpointId) : ['user', 'endpoint-keys', 'none'],
    queryFn: async () => {
      if (!endpointId) return [];
      return listPayload(
        await apiFetch<unknown>(`/api/endpoints/${encodeURIComponent(endpointId)}/keys`),
      ).map(normalizeEndpointKey);
    },
    enabled: enabled && Boolean(endpointId),
  });
}

export function useKeyModels(
  endpointId: string | undefined,
  keyId: string | undefined,
  enabled = true,
) {
  return useQuery({
    queryKey:
      endpointId && keyId
        ? userKeys.keyModels(endpointId, keyId)
        : ['user', 'key-models', 'none', 'none'],
    queryFn: async () => {
      if (!endpointId || !keyId) return [];
      return listPayload(
        await apiFetch<unknown>(
          `/api/endpoints/${encodeURIComponent(endpointId)}/keys/${encodeURIComponent(keyId)}/models`,
        ),
      ).map(normalizeUpstreamModel);
    },
    enabled: enabled && Boolean(endpointId && keyId),
  });
}

export function usePlatformModels(enabled = true) {
  return useQuery({
    queryKey: userKeys.models,
    queryFn: async () => listPayload(await apiFetch<unknown>('/api/models')).map(normalizePlatformModel),
    enabled,
  });
}

export function useModelBindings(modelId: string | undefined, enabled = true) {
  return useQuery({
    queryKey: modelId ? userKeys.bindings(modelId) : ['user', 'bindings', 'none'],
    queryFn: async () => {
      if (!modelId) return [];
      return listPayload(
        await apiFetch<unknown>(`/api/models/${encodeURIComponent(modelId)}/bindings`),
      ).map(normalizeBinding);
    },
    enabled: enabled && Boolean(modelId),
  });
}

export function useCallerKey(enabled = true) {
  return useQuery({
    queryKey: userKeys.callerKey,
    queryFn: async () => normalizeCallerKey(await apiFetch<unknown>('/api/caller-key')),
    enabled,
    staleTime: 5_000,
  });
}

/**
 * The user issue center page. resolved is a tri-state: undefined shows every
 * issue, true only acknowledged ones, false only open ones. Pagination is
 * server-side offset paging (page + page_size) inside a `{data, has_more}`
 * envelope, so any page can be sought directly.
 */
export function useUserIssues(page: number, resolved?: boolean, pageSize = 20, enabled = true) {
  const query = new URLSearchParams({ page: String(page), page_size: String(pageSize) });
  if (resolved !== undefined) query.set('resolved', resolved ? 'true' : 'false');
  const filterKey = resolved === undefined ? 'all' : String(resolved);
  return useQuery({
    queryKey: [...userKeys.issues, filterKey, page, pageSize],
    queryFn: async () =>
      normalizeIssuePage(await apiFetch<unknown>(`/api/issues?${query}`), pageSize),
    enabled,
  });
}

/** Acknowledge one issue; the server treats repeats as a success. */
export function useResolveUserIssue() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (issueId: string) =>
      apiFetch<void>(`/api/issues/${encodeURIComponent(issueId)}/resolve`, { method: 'POST' }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: userKeys.issues });
    },
  });
}
