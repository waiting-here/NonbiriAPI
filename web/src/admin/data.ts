import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ApiError, apiFetch } from '@shared/query/http';
import {
  asArray,
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
  /** Nullable ban deadline as unix seconds; absent while there is none. */
  banned_until?: number;
  endpoint_limit?: number;
  rpm_limit?: number;
  total_requests: number;
  total_prompt_tokens: number;
  total_completion_tokens: number;
  total_unknown_usage_requests: number;
  /** Canonical decimal milli-credit string; the signed consumption balance. */
  credits_balance: string;
  /** Canonical decimal milli-credit string; the non-negative donor reward. */
  donation_credit_balance: string;
  /** Manual level override (1..5); absent means no override (automatic). */
  level?: number;
  /** Persisted automatic high-water level (1..4). */
  auto_level: number;
  created_at: string;
}

/**
 * One metadata-only administrator log row (frozen projection). There is no
 * user-chosen platform model name and no note field on this shape; the
 * normalizer whitelists exactly these fields, so anything else a faulty
 * server might send is dropped before it can reach the query cache.
 */
export interface AdminRequestLog {
  id: string;
  user_id: string;
  route_kind: string;
  endpoint_base_url: string;
  endpoint_key_id: string;
  upstream_model_id: string;
  status_code: number;
  duration_ms: number;
  /** Unix seconds. */
  started_at: number;
  /** Unix seconds. */
  completed_at: number;
  uncached_input_tokens: number;
  cache_write_input_tokens: number;
  cache_read_input_tokens: number;
  output_tokens: number;
  prompt_tokens: number;
  completion_tokens: number;
  total_tokens: number;
  usage_unknown: boolean;
  error_code: string;
  error_source: string;
  error_diag: string;
  attempt_id: string;
}

/** Frozen administrator filter set — exact equality on the server. */
export interface AdminLogFilter {
  userId?: string;
  endpointBaseURL?: string;
  upstreamModel?: string;
  errorCode?: string;
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

/** One user's expandable entry inside a grouped overview row (frozen metadata only). */
export interface AdminEndpointUserSummary {
  user_id: string;
  endpoint_count: number;
  key_count: number;
  enabled_count: number;
}

/**
 * One canonical base_url's admin overview group. The normalizer whitelists
 * exactly these fields, so a note, username, Discord identifier, or secret
 * can never enter the request cache even if a faulty server sent one.
 */
export interface AdminEndpointOverviewGroup {
  base_url: string;
  user_count: number;
  endpoint_count: number;
  key_count: number;
  users: AdminEndpointUserSummary[];
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
  users: (page: number, pageSize: number, isBanned?: boolean, q?: string) =>
    [
      'admin',
      'users',
      page,
      pageSize,
      isBanned === undefined ? 'all' : String(isBanned),
      q ?? '',
    ] as const,
  usersRoot: ['admin', 'users'] as const,
  logs: (page: number, filter: AdminLogFilter, pageSize?: number) =>
    [
      'admin',
      'logs',
      page,
      filter.userId ?? '',
      filter.endpointBaseURL ?? '',
      filter.upstreamModel ?? '',
      filter.errorCode ?? '',
      filter.status ?? '',
      filter.fromUnix ?? '',
      filter.toUnix ?? '',
      pageSize ?? ADMIN_PAGE_SIZE,
    ] as const,
  logsRoot: ['admin', 'logs'] as const,
  usage: ['admin', 'usage'] as const,
  endpoints: (page: number, pageSize: number, filter: string) =>
    ['admin', 'endpoints', page, pageSize, filter] as const,
  alerts: (page: number, resolved?: boolean, pageSize?: number) =>
    [
      'admin',
      'alerts',
      page,
      resolved === undefined ? 'all' : String(resolved),
      pageSize ?? ADMIN_PAGE_SIZE,
    ] as const,
  alertsRoot: ['admin', 'alerts'] as const,
  siteConfig: ['admin', 'site-config'] as const,
};

function recordValue(record: UnknownRecord, key: string): unknown {
  return record[key];
}

// Economy amounts stay canonical decimal milli-credit strings end to end (the
// shared formatter renders them; they never pass through Number()). A
// malformed value stays '' and renders as a placeholder.
function amountValue(value: unknown): string {
  return typeof value === 'string' && value.length <= 32 ? value : '';
}

// Nullable integer projection: present only for a finite number.
function optionalNumberValue(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isFinite(value) ? value : undefined;
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
  const bannedUntil = optionalNumberValue(recordValue(record, 'banned_until'));
  const level = optionalNumberValue(recordValue(record, 'level'));
  const autoLevel = optionalNumberValue(recordValue(record, 'auto_level'));
  return {
    id: idValue(recordValue(record, 'id')),
    username: text(recordValue(record, 'username'), 128, '—'),
    ...(optionalText(recordValue(record, 'avatar'), 512)
      ? { avatar: optionalText(recordValue(record, 'avatar'), 512) }
      : {}),
    discord_id: text(recordValue(record, 'discord_id'), 128, '—'),
    is_banned: booleanValue(recordValue(record, 'is_banned')),
    banned_reason: text(recordValue(record, 'banned_reason'), 512),
    ...(bannedUntil !== undefined && bannedUntil >= 0
      ? { banned_until: Math.trunc(bannedUntil) }
      : {}),
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
    credits_balance: amountValue(recordValue(record, 'credits_balance')),
    donation_credit_balance: amountValue(recordValue(record, 'donation_credit_balance')),
    ...(level !== undefined && level >= 1 && level <= 5 ? { level: Math.trunc(level) } : {}),
    auto_level:
      autoLevel !== undefined && autoLevel >= 1 && autoLevel <= 4 ? Math.trunc(autoLevel) : 1,
    created_at: dateValue(recordValue(record, 'created_at')),
  };
}

function unixSecondsValue(value: unknown): number {
  if (typeof value === 'number' && Number.isFinite(value) && value >= 0) {
    return Math.trunc(value);
  }
  return 0;
}

function normalizeLog(payload: unknown): AdminRequestLog {
  const record = asRecord(payload) ?? {};
  const bucket = (key: string): number => {
    const raw = recordValue(record, key);
    return typeof raw === 'number' && Number.isSafeInteger(raw) && raw >= 0 ? raw : 0;
  };
  return {
    id: idValue(recordValue(record, 'id')),
    user_id: idValue(recordValue(record, 'user_id')),
    route_kind: text(recordValue(record, 'route_kind'), 64, '—'),
    endpoint_base_url: text(recordValue(record, 'endpoint_base_url'), 2048, '—'),
    endpoint_key_id: idValue(recordValue(record, 'endpoint_key_id')),
    upstream_model_id: text(recordValue(record, 'upstream_model_id'), 256, '—'),
    status_code: Math.max(0, integerValue(recordValue(record, 'status_code'))),
    duration_ms: Math.max(0, integerValue(recordValue(record, 'duration_ms'))),
    started_at: unixSecondsValue(recordValue(record, 'started_at')),
    completed_at: unixSecondsValue(recordValue(record, 'completed_at')),
    uncached_input_tokens: bucket('uncached_input_tokens'),
    cache_write_input_tokens: bucket('cache_write_input_tokens'),
    cache_read_input_tokens: bucket('cache_read_input_tokens'),
    output_tokens: bucket('output_tokens'),
    prompt_tokens: bucket('prompt_tokens'),
    completion_tokens: bucket('completion_tokens'),
    total_tokens: bucket('total_tokens'),
    usage_unknown: booleanValue(recordValue(record, 'usage_unknown')),
    error_code: text(recordValue(record, 'error_code'), 96),
    error_source: text(recordValue(record, 'error_source'), 32),
    error_diag: text(recordValue(record, 'error_diag'), 4096),
    attempt_id: text(recordValue(record, 'attempt_id'), 128),
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

function normalizeEndpointUser(payload: unknown): AdminEndpointUserSummary {
  const record = asRecord(payload) ?? {};
  return {
    user_id: idValue(recordValue(record, 'user_id')),
    endpoint_count: Math.max(0, integerValue(recordValue(record, 'endpoint_count'))),
    key_count: Math.max(0, integerValue(recordValue(record, 'key_count'))),
    enabled_count: Math.max(0, integerValue(recordValue(record, 'enabled_count'))),
  };
}

// Field whitelist only: any extra server field (note, username, Discord ID,
// secret material) is dropped here before it can reach the query cache.
function normalizeEndpointGroup(payload: unknown): AdminEndpointOverviewGroup {
  const record = asRecord(payload) ?? {};
  return {
    base_url: text(recordValue(record, 'base_url'), 2048, '—'),
    user_count: Math.max(0, integerValue(recordValue(record, 'user_count'))),
    endpoint_count: Math.max(0, integerValue(recordValue(record, 'endpoint_count'))),
    key_count: Math.max(0, integerValue(recordValue(record, 'key_count'))),
    users: asArray(recordValue(record, 'users')).map(normalizeEndpointUser),
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

export function useAdminUsers(
  page: number,
  pageSize = ADMIN_PAGE_SIZE,
  isBanned?: boolean,
  q?: string,
  enabled = true,
) {
  const params = new URLSearchParams({ page: String(page), page_size: String(pageSize) });
  if (isBanned !== undefined) params.set('is_banned', isBanned ? 'true' : 'false');
  if (q) params.set('q', q);
  return useQuery({
    queryKey: adminKeys.users(page, pageSize, isBanned, q),
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

/**
 * Server-paginated administrator log list. The frozen filter set is exact
 * equality on the server; the client sends only non-empty single values and
 * never a platform model name or note filter.
 */
export function useAdminLogs(
  page: number,
  filter: AdminLogFilter = {},
  pageSize = ADMIN_PAGE_SIZE,
  enabled = true,
) {
  const params = new URLSearchParams({ page: String(page), page_size: String(pageSize) });
  if (filter.userId) params.set('user_id', filter.userId);
  if (filter.endpointBaseURL) params.set('endpoint_base_url', filter.endpointBaseURL);
  if (filter.upstreamModel) params.set('upstream_model', filter.upstreamModel);
  if (filter.errorCode) params.set('error_code', filter.errorCode);
  if (filter.status) params.set('status', filter.status);
  if (filter.fromUnix !== undefined) params.set('from', String(filter.fromUnix));
  if (filter.toUnix !== undefined) params.set('to', String(filter.toUnix));
  return useQuery({
    queryKey: adminKeys.logs(page, filter, pageSize),
    queryFn: async () => {
      const result = pagePayload(await apiFetch<unknown>(`/admin/api/logs?${params}`), pageSize);
      return {
        items: result.items.map(normalizeLog),
        hasNext: result.hasNext,
        ...(result.nextCursor ? { nextCursor: result.nextCursor } : {}),
      } satisfies AdminPage<AdminRequestLog>;
    },
    enabled,
  });
}

/**
 * Builds the admin export download path (CSV or JSON) for the current
 * filter. Export endpoints reject paging parameters, so none are sent; the
 * browser only ever follows this link — it never parses or renders the file.
 */
export function adminLogExportPath(filter: AdminLogFilter, format: 'csv' | 'json'): string {
  const params = new URLSearchParams();
  if (filter.userId) params.set('user_id', filter.userId);
  if (filter.endpointBaseURL) params.set('endpoint_base_url', filter.endpointBaseURL);
  if (filter.upstreamModel) params.set('upstream_model', filter.upstreamModel);
  if (filter.errorCode) params.set('error_code', filter.errorCode);
  if (filter.status) params.set('status', filter.status);
  if (filter.fromUnix !== undefined) params.set('from', String(filter.fromUnix));
  if (filter.toUnix !== undefined) params.set('to', String(filter.toUnix));
  const query = params.toString();
  return `/admin/api/logs/export.${format}${query ? `?${query}` : ''}`;
}

export function useAdminUsage(enabled = true) {
  return useQuery({
    queryKey: adminKeys.usage,
    queryFn: async () => normalizeUsage(await apiFetch<unknown>('/admin/api/usage')),
    enabled,
  });
}

/**
 * Server-paginated grouped endpoint overview. Page, page size, and the
 * literal base_url substring filter are all server-evaluated; the client
 * never rebuilds a full cross-user list and never merges URLs itself.
 */
export function useAdminEndpointOverview(
  page: number,
  pageSize = ADMIN_PAGE_SIZE,
  filter = '',
  enabled = true,
) {
  const params = new URLSearchParams({ page: String(page), page_size: String(pageSize) });
  if (filter) params.set('filter', filter);
  return useQuery({
    queryKey: adminKeys.endpoints(page, pageSize, filter),
    queryFn: async () => {
      const payload = await apiFetch<unknown>(`/admin/api/overview/endpoints?${params}`);
      if (!isListPayload(payload)) {
        throw new ApiError('invalid_response', 'The server returned an invalid list.', 200);
      }
      const result = listResult(payload, pageSize);
      return {
        items: result.items.map(normalizeEndpointGroup),
        hasMore: result.hasNext,
      } satisfies { items: AdminEndpointOverviewGroup[]; hasMore: boolean };
    },
    enabled,
  });
}

export function useAdminAlerts(
  page: number,
  resolved?: boolean,
  pageSize = ADMIN_PAGE_SIZE,
  enabled = true,
) {
  const params = new URLSearchParams({ page: String(page), page_size: String(pageSize) });
  if (resolved !== undefined) params.set('resolved', resolved ? 'true' : 'false');
  return useQuery({
    queryKey: adminKeys.alerts(page, resolved, pageSize),
    queryFn: async () => {
      const result = pagePayload(await apiFetch<unknown>(`/admin/api/alerts?${params}`), pageSize);
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

export interface AdminCreditAdjustmentInput {
  userId: string;
  /** Canonical decimal milli-credit delta; omitted when the field is blank. */
  creditsDelta?: string;
  /** Canonical decimal milli-credit delta; omitted when the field is blank. */
  donationCreditDelta?: string;
  /**
   * Idempotency key. One key per logical confirmation: a retry of the same
   * submission reuses it verbatim (the server returns the first application's
   * result), a changed submission gets a fresh key.
   */
  operationId: string;
  /** Bounded, non-empty reason required by the server for every adjustment. */
  reason: string;
}

/**
 * The economy mode of PATCH /admin/api/users/{id}: canonical decimal-string
 * DELTAS (never target balances) plus the mandatory operation_id and reason.
 * The request carries no profile/ban field — the modes are mutually exclusive
 * server-side. The response's credit balances are the balances as of THIS
 * operation's application (the first one on an operation-id replay), so the
 * returned row is rendered as-is; the user list is refetched, never patched.
 */
export function useAdminAdjustUserCredits() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (input: AdminCreditAdjustmentInput) => {
      const json: Record<string, string> = {
        operation_id: input.operationId,
        reason: input.reason,
      };
      if (input.creditsDelta !== undefined) json.credits = input.creditsDelta;
      if (input.donationCreditDelta !== undefined) json.donation_credit = input.donationCreditDelta;
      const payload = await apiFetch<unknown>(
        `/admin/api/users/${encodeURIComponent(input.userId)}`,
        { method: 'PATCH', json },
      );
      if (!asRecord(payload)) {
        throw new ApiError('invalid_response', 'The server returned an invalid user.', 200);
      }
      return normalizeUser(payload);
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: adminKeys.usersRoot });
    },
  });
}

/**
 * The level tri-state of the profile mode: a number 1..5 sets the manual
 * override, null resets to the automatic high-water mark. Only the level
 * field is sent (profile mode must not mix with economy fields).
 */
export function useAdminUpdateUserLevel() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async ({ userId, level }: { userId: string; level: number | null }) => {
      const payload = await apiFetch<unknown>(`/admin/api/users/${encodeURIComponent(userId)}`, {
        method: 'PATCH',
        json: { level },
      });
      if (!asRecord(payload)) {
        throw new ApiError('invalid_response', 'The server returned an invalid user.', 200);
      }
      return normalizeUser(payload);
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: adminKeys.usersRoot });
    },
  });
}

export interface AdminBanInput {
  userId: string;
  /** Optional bounded reason. */
  reason?: string;
  /**
   * Ban lifetime in seconds. Omitted (undefined) means a permanent ban; the
   * server stores the resulting absolute deadline and reports banned_until.
   */
  durationSeconds?: number;
}

/** POST /admin/api/users/{id}/ban — sessions and caller keys die atomically. */
export function useAdminBanUser() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ userId, reason, durationSeconds }: AdminBanInput) =>
      apiFetch<void>(`/admin/api/users/${encodeURIComponent(userId)}/ban`, {
        method: 'POST',
        json: {
          ...(reason ? { reason } : {}),
          ...(durationSeconds !== undefined ? { duration_seconds: durationSeconds } : {}),
        },
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: adminKeys.usersRoot });
    },
  });
}

/** POST /admin/api/users/{id}/unban — no body; the user re-issues their key. */
export function useAdminUnbanUser() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (userId: string) =>
      apiFetch<void>(`/admin/api/users/${encodeURIComponent(userId)}/unban`, { method: 'POST' }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: adminKeys.usersRoot });
    },
  });
}
