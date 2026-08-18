import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ApiError, apiFetch } from '@shared/query/http';
import {
  asRecord,
  booleanValue,
  dateValue,
  idValue,
  integerValue,
  isListPayload,
  listResult,
  optionalText,
  text,
  type UnknownRecord,
} from '@shared/query/normalize';

export const ADMIN_PAGE_SIZE = 20;

export interface AdminPage<T> {
  items: T[];
  hasNext: boolean;
  nextCursor?: string;
}

export interface AdminSessionResponse {
  admin: { username: string };
}

export interface AdminUser {
  id: string;
  username: string;
  avatar?: string;
  discord_id: string;
  is_banned: boolean;
  banned_reason: string;
  endpoint_limit?: number;
  rpm_limit?: number;
  total_requests: number;
  total_prompt_tokens: number;
  total_completion_tokens: number;
  total_unknown_usage_requests: number;
  created_at: string;
}

export interface AdminLog {
  id: string;
  user_id: string;
  model: string;
  endpoint_key_id: string;
  upstream_model_id: string;
  status_code: number;
  duration_ms: number;
  started_at: string;
  completed_at: string;
  prompt_tokens?: number;
  completion_tokens?: number;
  total_tokens?: number;
  usage_unknown: boolean;
  error_code: string;
  error_diag: string;
}

export interface AdminLogFilter {
  userId?: string;
  model?: string;
  status?: string;
  fromUnix?: number;
  toUnix?: number;
}

export interface AdminUsage {
  total_requests: number;
  total_prompt_tokens: number;
  total_completion_tokens: number;
  total_unknown_usage_requests: number;
}

export interface AdminEndpointOverview {
  id: string;
  user_id: string;
  connector_type: string;
  base_url: string;
  note: string;
  enabled: boolean;
  key_count: number;
  created_at: string;
  updated_at: string;
}

export interface AdminModelOverview {
  id: string;
  user_id: string;
  provider: string;
  model: string;
  full_name: string;
  route_strategy: 'ordered' | 'random';
  silent_retry: boolean;
  binding_count: number;
  created_at: string;
}

export interface AdminAlert {
  id: string;
  kind: string;
  message: string;
  ref: string;
  subject_user_id: string;
  created_at: string;
  resolved: boolean;
  resolved_at: string;
}

export type SiteConfigValue = string | number | boolean | null;
export type SiteConfig = Record<string, SiteConfigValue>;

export const adminKeys = {
  all: ['admin'] as const,
  session: ['admin', 'session'] as const,
  users: (page: number, pageSize: number, isBanned?: boolean) =>
    ['admin', 'users', page, pageSize, isBanned === undefined ? 'all' : String(isBanned)] as const,
  usersRoot: ['admin', 'users'] as const,
  logs: (page: number, filter: AdminLogFilter, beforeId?: string, limit?: number) =>
    [
      'admin',
      'logs',
      page,
      filter.userId ?? '',
      filter.model ?? '',
      filter.status ?? '',
      filter.fromUnix ?? '',
      filter.toUnix ?? '',
      beforeId ?? '',
      limit ?? ADMIN_PAGE_SIZE,
    ] as const,
  logsRoot: ['admin', 'logs'] as const,
  usage: ['admin', 'usage'] as const,
  endpoints: ['admin', 'endpoints'] as const,
  models: ['admin', 'models'] as const,
  alerts: (page: number, resolved?: boolean, beforeId?: string, limit?: number) =>
    [
      'admin',
      'alerts',
      page,
      resolved === undefined ? 'all' : String(resolved),
      beforeId ?? '',
      limit ?? ADMIN_PAGE_SIZE,
    ] as const,
  alertsRoot: ['admin', 'alerts'] as const,
  siteConfig: ['admin', 'site-config'] as const,
};

function recordValue(record: UnknownRecord, key: string): unknown {
  return record[key];
}

interface PagePayload {
  items: unknown[];
  hasNext: boolean;
  nextCursor?: string;
}

function pagePayload(payload: unknown, pageSize = ADMIN_PAGE_SIZE): PagePayload {
  if (!isListPayload(payload)) {
    throw new ApiError('invalid_response', 'The server returned an invalid list.', 200);
  }
  const record = asRecord(payload);
  const result = listResult(payload, pageSize + 1);
  const explicitHasNext = record?.has_more ?? record?.hasMore ?? record?.next_page;
  const hasNext =
    typeof explicitHasNext === 'boolean'
      ? explicitHasNext
      : typeof explicitHasNext === 'number'
        ? explicitHasNext > 0
        : result.items.length > pageSize;
  const items = result.items.slice(0, pageSize);
  const lastRecord = asRecord(items[items.length - 1]);
  const cursor = lastRecord ? idValue(recordValue(lastRecord, 'id')) : undefined;
  return {
    items,
    hasNext,
    ...(hasNext && cursor && cursor !== '—' ? { nextCursor: cursor } : {}),
  };
}

function listPayload(payload: unknown): unknown[] {
  if (!isListPayload(payload)) {
    throw new ApiError('invalid_response', 'The server returned an invalid list.', 200);
  }
  return listResult(payload, 1000).items;
}

function normalizeSession(payload: unknown): AdminSessionResponse {
  const record = asRecord(payload);
  const admin = record ? asRecord(record.admin) : null;
  if (!admin) throw new ApiError('invalid_response', 'The server returned an invalid session.', 200);
  const username = text(recordValue(admin, 'username'), 128);
  if (!username) throw new ApiError('invalid_response', 'The server returned an invalid session.', 200);
  return { admin: { username } };
}

function normalizeUser(payload: unknown): AdminUser {
  const record = asRecord(payload) ?? {};
  const endpointLimit = recordValue(record, 'endpoint_limit');
  const rpmLimit = recordValue(record, 'rpm_limit');
  return {
    id: idValue(recordValue(record, 'id')),
    username: text(recordValue(record, 'username'), 128, '—'),
    ...(optionalText(recordValue(record, 'avatar'), 512)
      ? { avatar: optionalText(recordValue(record, 'avatar'), 512) }
      : {}),
    discord_id: text(recordValue(record, 'discord_id'), 128, '—'),
    is_banned: booleanValue(recordValue(record, 'is_banned')),
    banned_reason: text(recordValue(record, 'banned_reason'), 512),
    ...(typeof endpointLimit === 'number' && Number.isFinite(endpointLimit)
      ? { endpoint_limit: Math.max(0, Math.trunc(endpointLimit)) }
      : {}),
    ...(typeof rpmLimit === 'number' && Number.isFinite(rpmLimit)
      ? { rpm_limit: Math.max(0, Math.trunc(rpmLimit)) }
      : {}),
    total_requests: Math.max(0, integerValue(recordValue(record, 'total_requests'))),
    total_prompt_tokens: Math.max(0, integerValue(recordValue(record, 'total_prompt_tokens'))),
    total_completion_tokens: Math.max(0, integerValue(recordValue(record, 'total_completion_tokens'))),
    total_unknown_usage_requests: Math.max(
      0,
      integerValue(recordValue(record, 'total_unknown_usage_requests')),
    ),
    created_at: dateValue(recordValue(record, 'created_at')),
  };
}

function normalizeLog(payload: unknown): AdminLog {
  const record = asRecord(payload) ?? {};
  const prompt = recordValue(record, 'prompt_tokens');
  const completion = recordValue(record, 'completion_tokens');
  const total = recordValue(record, 'total_tokens');
  return {
    id: idValue(recordValue(record, 'id')),
    user_id: idValue(recordValue(record, 'user_id')),
    model: text(recordValue(record, 'model'), 256, '—'),
    endpoint_key_id: idValue(recordValue(record, 'endpoint_key_id')),
    upstream_model_id: text(recordValue(record, 'upstream_model_id'), 256, '—'),
    status_code: Math.max(0, integerValue(recordValue(record, 'status_code'))),
    duration_ms: Math.max(0, integerValue(recordValue(record, 'duration_ms'))),
    started_at: dateValue(recordValue(record, 'started_at')),
    completed_at: dateValue(recordValue(record, 'completed_at')),
    ...(typeof prompt === 'number' && Number.isFinite(prompt)
      ? { prompt_tokens: Math.max(0, Math.trunc(prompt)) }
      : {}),
    ...(typeof completion === 'number' && Number.isFinite(completion)
      ? { completion_tokens: Math.max(0, Math.trunc(completion)) }
      : {}),
    ...(typeof total === 'number' && Number.isFinite(total)
      ? { total_tokens: Math.max(0, Math.trunc(total)) }
      : {}),
    usage_unknown: booleanValue(recordValue(record, 'usage_unknown')),
    error_code: text(recordValue(record, 'error_code'), 96),
    error_diag: text(recordValue(record, 'error_diag'), 4096),
  };
}

function normalizeUsage(payload: unknown): AdminUsage {
  const record = asRecord(payload);
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

function normalizeEndpoint(payload: unknown): AdminEndpointOverview {
  const record = asRecord(payload) ?? {};
  return {
    id: idValue(recordValue(record, 'id')),
    user_id: idValue(recordValue(record, 'user_id')),
    connector_type: text(recordValue(record, 'connector_type'), 96, 'openai-compatible'),
    base_url: text(recordValue(record, 'base_url'), 2048, '—'),
    note: text(recordValue(record, 'note'), 512),
    enabled: booleanValue(recordValue(record, 'enabled'), true),
    key_count: Math.max(0, integerValue(recordValue(record, 'key_count'))),
    created_at: dateValue(recordValue(record, 'created_at')),
    updated_at: dateValue(recordValue(record, 'updated_at')),
  };
}

function normalizeModel(payload: unknown): AdminModelOverview {
  const record = asRecord(payload) ?? {};
  return {
    id: idValue(recordValue(record, 'id')),
    user_id: idValue(recordValue(record, 'user_id')),
    provider: text(recordValue(record, 'provider'), 64, '—'),
    model: text(recordValue(record, 'model'), 64, '—'),
    full_name: text(recordValue(record, 'full_name'), 160, '—'),
    route_strategy: recordValue(record, 'route_strategy') === 'random' ? 'random' : 'ordered',
    silent_retry: booleanValue(recordValue(record, 'silent_retry')),
    binding_count: Math.max(0, integerValue(recordValue(record, 'binding_count'))),
    created_at: dateValue(recordValue(record, 'created_at')),
  };
}

function normalizeAlert(payload: unknown): AdminAlert {
  const record = asRecord(payload) ?? {};
  return {
    id: idValue(recordValue(record, 'id')),
    kind: text(recordValue(record, 'kind'), 96, '—'),
    message: text(recordValue(record, 'message'), 1024, '—'),
    ref: text(recordValue(record, 'ref'), 256),
    subject_user_id: idValue(recordValue(record, 'subject_user_id')),
    created_at: dateValue(recordValue(record, 'created_at')),
    resolved: booleanValue(recordValue(record, 'resolved')),
    resolved_at: dateValue(recordValue(record, 'resolved_at')),
  };
}

function normalizeSiteConfig(payload: unknown): SiteConfig {
  const record = asRecord(payload) ?? {};
  const result: SiteConfig = {};
  for (const [rawKey, raw] of Object.entries(record)) {
    const key = text(rawKey, 96);
    if (!key) continue;
    if (typeof raw === 'string') result[key] = text(raw, 512);
    else if (typeof raw === 'number' && Number.isFinite(raw)) result[key] = raw;
    else if (typeof raw === 'boolean' || raw === null) result[key] = raw;
  }
  return result;
}

export function useAdminSession() {
  return useQuery({
    queryKey: adminKeys.session,
    queryFn: async () => normalizeSession(await apiFetch<unknown>('/admin/api/session')),
    staleTime: 15_000,
    retry: false,
  });
}

export function useAdminUsers(page: number, pageSize = ADMIN_PAGE_SIZE, isBanned?: boolean, enabled = true) {
  const params = new URLSearchParams({ page: String(page), page_size: String(pageSize) });
  if (isBanned !== undefined) params.set('is_banned', isBanned ? 'true' : 'false');
  return useQuery({
    queryKey: adminKeys.users(page, pageSize, isBanned),
    queryFn: async () => {
      const result = pagePayload(
        await apiFetch<unknown>(`/admin/api/users?${params}`),
        pageSize,
      );
      return {
        items: result.items.map(normalizeUser),
        hasNext: result.hasNext,
        ...(result.nextCursor ? { nextCursor: result.nextCursor } : {}),
      } satisfies AdminPage<AdminUser>;
    },
    enabled,
  });
}

export function useAdminLogs(
  page: number,
  filter: AdminLogFilter = {},
  beforeId?: string,
  limit = ADMIN_PAGE_SIZE,
  enabled = true,
) {
  const params = new URLSearchParams({ limit: String(limit + 1) });
  if (filter.userId) params.set('user_id', filter.userId);
  if (filter.model) params.set('model', filter.model);
  if (filter.status) params.set('status', filter.status);
  if (filter.fromUnix !== undefined) params.set('from', String(filter.fromUnix));
  if (filter.toUnix !== undefined) params.set('to', String(filter.toUnix));
  if (beforeId) params.set('before_id', beforeId);
  return useQuery({
    queryKey: adminKeys.logs(page, filter, beforeId, limit),
    queryFn: async () => {
      const result = pagePayload(await apiFetch<unknown>(`/admin/api/logs?${params}`), limit);
      return {
        items: result.items.map(normalizeLog),
        hasNext: result.hasNext,
        ...(result.nextCursor ? { nextCursor: result.nextCursor } : {}),
      } satisfies AdminPage<AdminLog>;
    },
    enabled,
  });
}

export function useAdminUsage(enabled = true) {
  return useQuery({
    queryKey: adminKeys.usage,
    queryFn: async () => normalizeUsage(await apiFetch<unknown>('/admin/api/usage')),
    enabled,
  });
}

export function useAdminEndpoints(enabled = true) {
  return useQuery({
    queryKey: adminKeys.endpoints,
    queryFn: async () =>
      listPayload(await apiFetch<unknown>('/admin/api/overview/endpoints')).map(normalizeEndpoint),
    enabled,
  });
}

export function useAdminModels(enabled = true) {
  return useQuery({
    queryKey: adminKeys.models,
    queryFn: async () =>
      listPayload(await apiFetch<unknown>('/admin/api/overview/models')).map(normalizeModel),
    enabled,
  });
}

export function useAdminAlerts(
  page: number,
  resolved?: boolean,
  beforeId?: string,
  limit = ADMIN_PAGE_SIZE,
  enabled = true,
) {
  const params = new URLSearchParams({ limit: String(limit + 1) });
  if (resolved !== undefined) params.set('resolved', resolved ? 'true' : 'false');
  if (beforeId) params.set('before_id', beforeId);
  return useQuery({
    queryKey: adminKeys.alerts(page, resolved, beforeId, limit),
    queryFn: async () => {
      const result = pagePayload(await apiFetch<unknown>(`/admin/api/alerts?${params}`), limit);
      return {
        items: result.items.map(normalizeAlert),
        hasNext: result.hasNext,
        ...(result.nextCursor ? { nextCursor: result.nextCursor } : {}),
      } satisfies AdminPage<AdminAlert>;
    },
    enabled,
  });
}

export function useSiteConfig(enabled = true) {
  return useQuery({
    queryKey: adminKeys.siteConfig,
    queryFn: async () => normalizeSiteConfig(await apiFetch<unknown>('/admin/api/site-config')),
    enabled,
    staleTime: 0,
  });
}

export function useResolveAdminAlert() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ alertId, resolved }: { alertId: string; resolved: boolean }) =>
      apiFetch<unknown>(`/admin/api/alerts/${encodeURIComponent(alertId)}/resolve`, {
        method: 'POST',
        json: { resolved },
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: adminKeys.alertsRoot });
    },
  });
}
