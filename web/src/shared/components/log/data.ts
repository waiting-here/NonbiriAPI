import { useQuery } from '@tanstack/react-query';
import { decoded, queryPath } from '@shared/operations/api';
import {
  amount,
  boolean,
  decimal,
  decimalID,
  invalidResponse,
  nullableDecimalID,
  nullableInteger,
  nullableString,
  nullableUnixSecond,
  oneOf,
  opaqueID,
  page,
  record,
  string,
  unixSecond,
  type CursorPage,
  type WireRecord,
} from '@shared/operations/wire';

export type LogRole = 'user' | 'admin' | 'steward';
export type LogRouteKind = 'openai_chat_completions' | 'charity_chat_completions' | 'model_discovery';
export type LogResultClass = 'success' | 'failed' | 'cancelled';

export interface LogUsage {
  uncached_input_tokens: string;
  cache_write_input_tokens: string;
  cache_read_input_tokens: string;
  output_tokens: string;
  total_tokens: string;
  usage_unknown: boolean;
  charge: string;
}

interface LogRowCommon {
  id: string;
  route_kind: LogRouteKind;
  caller_result_class: LogResultClass | null;
  caller_status: number | null;
  caller_error_code: string | null;
  started_at: number;
  completed_at: number | null;
  usage: LogUsage;
}

export interface UserSelfLogRow extends LogRowCommon {
  role: 'user';
  kind: 'self';
  route_kind: 'openai_chat_completions' | 'model_discovery';
  model: string;
  attempt_count: string;
}

export interface UserCharityLogRow extends LogRowCommon {
  role: 'user';
  kind: 'charity';
  route_kind: 'charity_chat_completions';
  model: string;
}

export type UserLogRow = UserSelfLogRow | UserCharityLogRow;

export interface AdminLogRow extends LogRowCommon {
  role: 'admin';
  user_id: string | null;
  attempt_count: string;
}

export interface StewardLogRow extends LogRowCommon {
  role: 'steward';
  attempt_count: string;
}

export type RoleLogRow = UserLogRow | AdminLogRow | StewardLogRow;

interface LogAttemptCommon {
  attempt_seq: string;
  result_kind: 'response' | 'synthetic';
  endpoint_key_id: string | null;
  endpoint_base_url: string;
  connector_type: 'openai-compatible' | 'anthropic-compatible';
  upstream_model_id: string;
  status_code: number | null;
  upstream_code: string | null;
  diag: string | null;
  usage: LogUsage;
  started_at: number;
  completed_at: number;
}

export interface UserLogAttempt extends LogAttemptCommon {
  role: 'user';
  endpoint_note: string;
  key_note: string;
}

export interface AdminLogAttempt extends LogAttemptCommon { role: 'admin' }
export interface StewardLogAttempt extends LogAttemptCommon { role: 'steward' }
export type RoleLogAttempt = UserLogAttempt | AdminLogAttempt | StewardLogAttempt;

export type UserLogDetail =
  | { kind: 'self'; request: UserSelfLogRow; attempts: CursorPage<UserLogAttempt> }
  | { kind: 'charity'; request: UserCharityLogRow; caller_safe_result: { class: LogResultClass } };
export interface AdminLogDetail { request: AdminLogRow; attempts: CursorPage<AdminLogAttempt> }
export interface StewardLogDetail { request: StewardLogRow; attempts: CursorPage<StewardLogAttempt> }
export type RoleLogDetail = UserLogDetail | AdminLogDetail | StewardLogDetail;

export interface LogFiltersValue {
  model?: string;
  user_id?: string;
  endpoint_base_url?: string;
  upstream_model?: string;
  error_code?: string;
  status?: string;
  from?: number;
  to?: number;
}

const ROUTE_KINDS = ['openai_chat_completions', 'charity_chat_completions', 'model_discovery'] as const;
const RESULT_CLASSES = ['success', 'failed', 'cancelled'] as const;
const COMMON_ROW_FIELDS = [
  'id', 'route_kind', 'caller_result_class', 'caller_status', 'caller_error_code',
  'started_at', 'completed_at', 'usage',
] as const;
const ATTEMPT_FIELDS = [
  'attempt_seq', 'result_kind', 'endpoint_key_id', 'endpoint_base_url', 'connector_type',
  'upstream_model_id', 'status_code', 'upstream_code', 'diag', 'usage', 'started_at', 'completed_at',
] as const;

export function normalizeLogUsage(value: unknown): LogUsage {
  const root = record(value, [
    'uncached_input_tokens', 'cache_write_input_tokens', 'cache_read_input_tokens',
    'output_tokens', 'total_tokens', 'usage_unknown', 'charge',
  ], 'log usage');
  const result = {
    uncached_input_tokens: decimal(root.uncached_input_tokens, 'uncached input tokens'),
    cache_write_input_tokens: decimal(root.cache_write_input_tokens, 'cache-write input tokens'),
    cache_read_input_tokens: decimal(root.cache_read_input_tokens, 'cache-read input tokens'),
    output_tokens: decimal(root.output_tokens, 'output tokens'),
    total_tokens: decimal(root.total_tokens, 'total tokens'),
    usage_unknown: boolean(root.usage_unknown, 'usage unknown marker'),
    charge: amount(root.charge, 'log charge', false),
  };
  const sum = BigInt(result.uncached_input_tokens) + BigInt(result.cache_write_input_tokens)
    + BigInt(result.cache_read_input_tokens) + BigInt(result.output_tokens);
  if (sum !== BigInt(result.total_tokens)) invalidResponse('log token total');
  return result;
}

function commonRow(root: WireRecord): LogRowCommon {
  const resultClass = root.caller_result_class === null
    ? null
    : oneOf(root.caller_result_class, RESULT_CLASSES, 'caller result class');
  const callerStatus = nullableInteger(root.caller_status, 'caller status', 100, 599);
  const callerErrorCode = nullableString(root.caller_error_code, 'caller error code', { min: 1, max: 64, bytes: 64, ascii: true });
  if (callerErrorCode !== null && !/^[a-z0-9_]+$/.test(callerErrorCode)) invalidResponse('caller error code');
  const startedAt = unixSecond(root.started_at, 'log start time');
  const completedAt = nullableUnixSecond(root.completed_at, 'log completion time');
  if (completedAt !== null && completedAt < startedAt) invalidResponse('log completion time');
  if (resultClass === null) {
    if (callerStatus !== null || callerErrorCode !== null || completedAt !== null) invalidResponse('nonterminal log result');
  } else if (resultClass === 'success') {
    if (completedAt === null || callerStatus === null || callerStatus < 200 || callerStatus > 399 || callerErrorCode !== null) invalidResponse('successful log result');
  } else if (resultClass === 'failed') {
    if (completedAt === null || callerStatus === null || callerStatus < 400 || callerErrorCode === null) invalidResponse('failed log result');
  } else if (completedAt === null || callerStatus !== null || callerErrorCode !== null) {
    invalidResponse('cancelled log result');
  }
  return {
    id: opaqueID(root.id, 'req_', 'request log id'),
    route_kind: oneOf(root.route_kind, ROUTE_KINDS, 'log route kind'),
    caller_result_class: resultClass,
    caller_status: callerStatus,
    caller_error_code: callerErrorCode,
    started_at: startedAt,
    completed_at: completedAt,
    usage: normalizeLogUsage(root.usage),
  };
}

export function normalizeUserLogRow(value: unknown): UserLogRow {
  const probe = record(value, [...COMMON_ROW_FIELDS, 'model', 'attempt_count'], 'user log row', [...COMMON_ROW_FIELDS, 'model']);
  const common = commonRow(probe);
  const model = string(probe.model, 'logical model', { max: 512, bytes: 2_048 });
  if (common.route_kind === 'charity_chat_completions') {
    if (Object.prototype.hasOwnProperty.call(probe, 'attempt_count')) invalidResponse('charity log row');
    return { ...common, role: 'user', kind: 'charity', route_kind: 'charity_chat_completions', model };
  }
  if (!Object.prototype.hasOwnProperty.call(probe, 'attempt_count')) invalidResponse('self log row');
  return {
    ...common,
    role: 'user',
    kind: 'self',
    route_kind: common.route_kind,
    model,
    attempt_count: decimal(probe.attempt_count, 'attempt count'),
  };
}

export function normalizeAdminLogRow(value: unknown): AdminLogRow {
  const root = record(value, [...COMMON_ROW_FIELDS, 'user_id', 'attempt_count'], 'administrator log row');
  return {
    ...commonRow(root),
    role: 'admin',
    user_id: nullableDecimalID(root.user_id, 'log user id'),
    attempt_count: decimal(root.attempt_count, 'attempt count'),
  };
}

export function normalizeStewardLogRow(value: unknown): StewardLogRow {
  const root = record(value, [...COMMON_ROW_FIELDS, 'attempt_count'], 'steward log row');
  return { ...commonRow(root), role: 'steward', attempt_count: decimal(root.attempt_count, 'attempt count') };
}

function commonAttempt(root: WireRecord): LogAttemptCommon {
  const endpointBaseURL = string(root.endpoint_base_url, 'endpoint base URL', { min: 1, max: 4_096, bytes: 4_096 });
  let parsed: URL;
  try { parsed = new URL(endpointBaseURL); } catch { return invalidResponse('endpoint base URL'); }
  if (!['http:', 'https:'].includes(parsed.protocol) || !parsed.hostname || parsed.username || parsed.password || parsed.search || parsed.hash) {
    invalidResponse('endpoint base URL');
  }
  const resultKind = oneOf(root.result_kind, ['response', 'synthetic'] as const, 'attempt result kind');
  const statusCode = nullableInteger(root.status_code, 'upstream status', 100, 599);
  if (resultKind === 'response' && statusCode === null) invalidResponse('response attempt status');
  const startedAt = unixSecond(root.started_at, 'attempt start time');
  const completedAt = unixSecond(root.completed_at, 'attempt completion time');
  if (completedAt < startedAt) invalidResponse('attempt completion time');
  return {
    attempt_seq: decimal(root.attempt_seq, 'attempt sequence', { positive: true }),
    result_kind: resultKind,
    endpoint_key_id: nullableDecimalID(root.endpoint_key_id, 'endpoint key id'),
    endpoint_base_url: endpointBaseURL,
    connector_type: oneOf(root.connector_type, ['openai-compatible', 'anthropic-compatible'] as const, 'connector type'),
    upstream_model_id: string(root.upstream_model_id, 'upstream model id', { max: 512, bytes: 2_048 }),
    status_code: statusCode,
    upstream_code: nullableString(root.upstream_code, 'upstream code', { min: 1, max: 64, bytes: 64, ascii: true }),
    diag: nullableString(root.diag, 'safe diagnostic', { max: 4_096, bytes: 4_096 }),
    usage: normalizeLogUsage(root.usage),
    started_at: startedAt,
    completed_at: completedAt,
  };
}

export function normalizeUserLogAttempt(value: unknown): UserLogAttempt {
  const root = record(value, [...ATTEMPT_FIELDS, 'endpoint_note', 'key_note'], 'user log attempt');
  return {
    ...commonAttempt(root),
    role: 'user',
    endpoint_note: string(root.endpoint_note, 'endpoint note', { max: 1_024, bytes: 4_096, multiline: true }),
    key_note: string(root.key_note, 'key note', { max: 1_024, bytes: 4_096, multiline: true }),
  };
}

export function normalizeAdminLogAttempt(value: unknown): AdminLogAttempt {
  const root = record(value, ATTEMPT_FIELDS, 'administrator log attempt');
  return { ...commonAttempt(root), role: 'admin' };
}

export function normalizeStewardLogAttempt(value: unknown): StewardLogAttempt {
  const root = record(value, ATTEMPT_FIELDS, 'steward log attempt');
  return { ...commonAttempt(root), role: 'steward' };
}

export function normalizeUserLogDetail(value: unknown): UserLogDetail {
  const probe = record(value, ['request', 'attempts', 'caller_safe_result'], 'user log detail', ['request']);
  const request = normalizeUserLogRow(probe.request);
  if (request.kind === 'charity') {
    const root = record(value, ['request', 'caller_safe_result'], 'charity log detail');
    const safe = record(root.caller_safe_result, ['class'], 'charity caller-safe result');
    return { kind: 'charity', request, caller_safe_result: { class: oneOf(safe.class, RESULT_CLASSES, 'caller-safe class') } };
  }
  const root = record(value, ['request', 'attempts'], 'self log detail');
  return { kind: 'self', request, attempts: page(root.attempts, 'self log attempts', normalizeUserLogAttempt) };
}

export function normalizeAdminLogDetail(value: unknown): AdminLogDetail {
  const root = record(value, ['request', 'attempts'], 'administrator log detail');
  return { request: normalizeAdminLogRow(root.request), attempts: page(root.attempts, 'administrator log attempts', normalizeAdminLogAttempt) };
}

export function normalizeStewardLogDetail(value: unknown): StewardLogDetail {
  const root = record(value, ['request', 'attempts'], 'steward log detail');
  return { request: normalizeStewardLogRow(root.request), attempts: page(root.attempts, 'steward log attempts', normalizeStewardLogAttempt) };
}

function rolePath(role: LogRole): string {
  if (role === 'admin') return '/admin/api/logs';
  if (role === 'steward') return '/api/steward/logs';
  return '/api/logs';
}

function roleDecoder(role: LogRole): (value: unknown) => RoleLogRow {
  if (role === 'admin') return normalizeAdminLogRow;
  if (role === 'steward') return normalizeStewardLogRow;
  return normalizeUserLogRow;
}

function logStationRoot(role: LogRole) {
  if (role === 'admin') return ['admin', 'operations', 'logs'] as const;
  if (role === 'steward') return ['user', 'operations', 'steward', 'logs'] as const;
  return ['user', 'operations', 'logs'] as const;
}

export const roleLogKeys = {
  root: (role: LogRole) => logStationRoot(role),
  list: (role: LogRole, cursor: string | null, limit: number, filter: LogFiltersValue) =>
    [...logStationRoot(role), 'list', cursor, limit, filter] as const,
  detail: (role: LogRole, id: string, cursor: string | null, limit: number) =>
    [...logStationRoot(role), 'detail', id, cursor, limit] as const,
};

export function useRoleLogs(
  role: LogRole,
  cursor: string | null,
  filter: LogFiltersValue,
  limit = 20,
  enabled = true,
) {
  return useQuery({
    queryKey: roleLogKeys.list(role, cursor, limit, filter),
    queryFn: () => decoded(
      queryPath(rolePath(role), { ...filter, cursor, limit }),
      (value) => page(value, `${role} logs`, roleDecoder(role)),
    ),
    enabled,
  });
}

export function useRoleLogDetail(
  role: LogRole,
  id: string | null,
  attemptCursor: string | null = null,
  attemptLimit = 20,
  enabled = true,
) {
  return useQuery({
    queryKey: roleLogKeys.detail(role, id ?? 'none', attemptCursor, attemptLimit),
    queryFn: async () => {
      if (!id) invalidResponse('log id');
      const detailPath = queryPath(`${rolePath(role)}/${encodeURIComponent(id)}`, {
        attempt_cursor: attemptCursor,
        attempt_limit: attemptLimit,
      });
      if (role === 'admin') return decoded(detailPath, normalizeAdminLogDetail);
      if (role === 'steward') return decoded(detailPath, normalizeStewardLogDetail);
      return decoded(detailPath, normalizeUserLogDetail);
    },
    enabled: enabled && Boolean(id),
  });
}

export function adminLogExportPath(filter: LogFiltersValue, format: 'csv' | 'json'): string {
  const adminFilter = { ...filter };
  delete adminFilter.model;
  return queryPath(`/admin/api/logs/export.${format}`, adminFilter);
}

export function validateLogFilter(role: LogRole, raw: Record<string, string>): LogFiltersValue {
  const result: LogFiltersValue = {};
  const assign = (key: keyof LogFiltersValue, max: number) => {
    const value = raw[key]?.trim();
    if (value) result[key] = Array.from(value).slice(0, max).join('') as never;
  };
  if (role === 'user') assign('model', 133);
  if (role === 'admin') {
    const userID = raw.user_id?.trim();
    if (userID && /^[1-9][0-9]*$/.test(userID)) result.user_id = decimalID(userID, 'user id filter');
  }
  if (role !== 'user') {
    assign('endpoint_base_url', 512);
    assign('upstream_model', 512);
  }
  assign('error_code', 96);
  const status = raw.status?.trim();
  if (status && /^(?:[1-5][0-9]{2})$/.test(status)) result.status = status;
  return result;
}
