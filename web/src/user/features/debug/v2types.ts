import { ApiError } from '@shared/query/http';
import {
  boolean,
  decimal,
  integer,
  invalidResponse,
  nullableInteger,
  nullableString,
  oneOf,
  opaqueID,
  record,
  string,
  unixSecond,
} from '@shared/operations/wire';

export type DebugMode = 'dry' | 'live';
export type DebugGapReason = 'cursor_invalid' | 'process_restart' | 'ring_expired' | 'ring_evicted' | 'slow_consumer';
export type DebugEndReason = 'stopped' | 'replaced' | 'idle_expired' | 'absolute_expired' | 'auth_revoked' | 'account_banned' | 'account_deleted' | 'shutdown';

const STABLE_ERROR_CODES = [
  'internal', 'invalid_request', 'unauthorized', 'forbidden', 'not_found', 'conflict',
  'method_not_allowed', 'rate_limited', 'payload_too_large', 'elevated_required',
  'unbound_model', 'upstream', 'maintenance', 'service_unavailable',
  'resource_limit_exceeded', 'resource_locked', 'insufficient_credits',
  'feature_disabled', 'charity_suspended', 'content_too_short', 'already_checked_in',
  'checkin_cap_reached', 'debug_dry_run_intercepted', 'debug_live_result_captured',
  'debug_live_cancelled',
] as const;

export interface DebugLimits {
  session_bytes: number;
  traces: number;
  events: number;
  subscribers: number;
  event_bytes: number;
  trace_bytes: number;
}

export type DebugSessionMetadata = { active: false } | {
  active: true;
  id: string;
  generation: string;
  revision: string;
  mode: DebugMode;
  created_at: number;
  expires_at: number;
  idle_expires_at: number;
  inflight_count: number;
  connected_subscribers: number;
  last_event_id: string | null;
  limits: DebugLimits;
};

export interface DebugUsage {
  uncached_input_tokens: string;
  cache_write_input_tokens: string;
  cache_read_input_tokens: string;
  output_tokens: string;
  total_tokens: string;
  usage_unknown: boolean;
  charge: string;
}

export interface DebugBody {
  media_type: string;
  byte_count: number;
  text: string | null;
  base64: string | null;
  truncated: boolean;
}

export interface DebugRequest {
  route_kind: 'openai_chat_completions' | 'charity_chat_completions';
  model: string;
  stream: boolean;
  body: DebugBody;
}

export interface DebugUpstreamResult {
  result_kind: 'response' | 'synthetic';
  status_code: number | null;
  upstream_code: string | null;
  diag: string | null;
  usage: DebugUsage;
  completed_at: number;
}

export interface DebugCallerResult {
  http_status: number;
  error_code: string | null;
  source: 'platform' | 'upstream';
  message: string;
  completed_at: number;
}

export interface DebugTrace {
  trace_id: string;
  revision: string;
  state: 'capturing' | 'terminal';
  request: DebugRequest;
  upstream_result: DebugUpstreamResult | null;
  caller_result: DebugCallerResult | null;
  created_at: number;
  updated_at: number;
  truncated: boolean;
}

export interface DebugSnapshotData {
  session: Extract<DebugSessionMetadata, { active: true }>;
  traces: DebugTrace[];
  first_event_id: string | null;
  last_event_id: string | null;
}

export type DebugEvent =
  | DebugEventBase<'snapshot', DebugSnapshotData>
  | DebugEventBase<'trace_upsert', DebugTrace>
  | DebugEventBase<'gap', { reason: DebugGapReason; first_available_event_id: string | null }>
  | DebugEventBase<'session_end', { reason: DebugEndReason; cancelled_inflight_count: number }>;

interface DebugEventBase<K extends string, D> {
  version: 2;
  event_id: string;
  session_id: string;
  generation: string;
  kind: K;
  occurred_at: number;
  data: D;
}

function debugAmount(value: unknown, label: string): string {
  if (typeof value !== 'string' || !/^(0|[1-9][0-9]*)(\.[0-9]{1,3})?$/.test(value)
    || (value.includes('.') && value.endsWith('0'))) invalidResponse(label);
  return value;
}

export function normalizeDebugUsage(value: unknown): DebugUsage {
  const root = record(value, [
    'uncached_input_tokens', 'cache_write_input_tokens', 'cache_read_input_tokens',
    'output_tokens', 'total_tokens', 'usage_unknown', 'charge',
  ], 'Debug usage');
  const result = {
    uncached_input_tokens: decimal(root.uncached_input_tokens, 'Debug uncached input tokens'),
    cache_write_input_tokens: decimal(root.cache_write_input_tokens, 'Debug cache-write input tokens'),
    cache_read_input_tokens: decimal(root.cache_read_input_tokens, 'Debug cache-read input tokens'),
    output_tokens: decimal(root.output_tokens, 'Debug output tokens'),
    total_tokens: decimal(root.total_tokens, 'Debug total tokens'),
    usage_unknown: boolean(root.usage_unknown, 'Debug unknown usage marker'),
    charge: debugAmount(root.charge, 'Debug charge'),
  };
  const total = BigInt(result.uncached_input_tokens) + BigInt(result.cache_write_input_tokens)
    + BigInt(result.cache_read_input_tokens) + BigInt(result.output_tokens);
  if (total !== BigInt(result.total_tokens)) invalidResponse('Debug token total');
  return result;
}

function normalizeDebugLimits(value: unknown): DebugLimits {
  const root = record(value, ['session_bytes', 'traces', 'events', 'subscribers', 'event_bytes', 'trace_bytes'], 'Debug limits');
  const result = {
    session_bytes: integer(root.session_bytes, 'Debug session byte limit'),
    traces: integer(root.traces, 'Debug trace limit'),
    events: integer(root.events, 'Debug event limit'),
    subscribers: integer(root.subscribers, 'Debug subscriber limit'),
    event_bytes: integer(root.event_bytes, 'Debug event byte limit'),
    trace_bytes: integer(root.trace_bytes, 'Debug trace byte limit'),
  };
  if (result.session_bytes !== 4_194_304 || result.traces !== 32 || result.events !== 128
    || result.subscribers !== 2 || result.event_bytes !== 524_288 || result.trace_bytes !== 786_432) {
    invalidResponse('Debug limits');
  }
  return result;
}

export function normalizeDebugSession(value: unknown): DebugSessionMetadata {
  const discriminator = record(value, [
    'active', 'id', 'generation', 'revision', 'mode', 'created_at', 'expires_at', 'idle_expires_at',
    'inflight_count', 'connected_subscribers', 'last_event_id', 'limits',
  ], 'Debug session', ['active']);
  if (discriminator.active === false) {
    if (Object.keys(discriminator).length !== 1) invalidResponse('inactive Debug session');
    return { active: false };
  }
  const root = record(value, [
    'active', 'id', 'generation', 'revision', 'mode', 'created_at', 'expires_at', 'idle_expires_at',
    'inflight_count', 'connected_subscribers', 'last_event_id', 'limits',
  ], 'active Debug session');
  if (root.active !== true) invalidResponse('Debug session discriminator');
  const createdAt = unixSecond(root.created_at, 'Debug creation time');
  const expiresAt = unixSecond(root.expires_at, 'Debug expiry');
  const idleExpiresAt = unixSecond(root.idle_expires_at, 'Debug idle expiry');
  if (idleExpiresAt < createdAt || expiresAt < idleExpiresAt) invalidResponse('Debug session deadlines');
  return {
    active: true,
    id: opaqueID(root.id, 'dbs_', 'Debug session id'),
    generation: decimal(root.generation, 'Debug generation', { positive: true }),
    revision: decimal(root.revision, 'Debug revision', { positive: true }),
    mode: oneOf(root.mode, ['dry', 'live'] as const, 'Debug mode'),
    created_at: createdAt,
    expires_at: expiresAt,
    idle_expires_at: idleExpiresAt,
    inflight_count: integer(root.inflight_count, 'Debug inflight count', 0, 32),
    connected_subscribers: integer(root.connected_subscribers, 'Debug subscriber count', 0, 2),
    last_event_id: root.last_event_id === null ? null : opaqueID(root.last_event_id, 'dbe_', 'Debug last event id'),
    limits: normalizeDebugLimits(root.limits),
  };
}

function boundedBodyText(value: unknown, label: string): string {
  if (typeof value !== 'string' || new TextEncoder().encode(value).byteLength > 786_432) invalidResponse(label);
  return value;
}

function decodedBase64Bytes(value: string): number {
  if (!/^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$/.test(value)) invalidResponse('Debug base64 body');
  try { return atob(value).length; } catch { return invalidResponse('Debug base64 body'); }
}

export function normalizeDebugBody(value: unknown): DebugBody {
  const root = record(value, ['media_type', 'byte_count', 'text', 'base64', 'truncated'], 'Debug request body');
  const textValue = root.text === null ? null : boundedBodyText(root.text, 'Debug text body');
  const base64Value = root.base64 === null ? null : boundedBodyText(root.base64, 'Debug base64 body');
  if ((textValue === null) === (base64Value === null)) invalidResponse('Debug request body representation');
  const byteCount = integer(root.byte_count, 'Debug request byte count', 0, Number.MAX_SAFE_INTEGER);
  const captured = textValue !== null ? new TextEncoder().encode(textValue).byteLength : decodedBase64Bytes(base64Value!);
  const truncated = boolean(root.truncated, 'Debug truncation marker');
  if (captured > byteCount || truncated !== (captured < byteCount)) invalidResponse('Debug request body length');
  return {
    media_type: string(root.media_type, 'Debug media type', { min: 1, max: 256, bytes: 256 }),
    byte_count: byteCount,
    text: textValue,
    base64: base64Value,
    truncated,
  };
}

export function normalizeDebugTrace(value: unknown): DebugTrace {
  const root = record(value, [
    'trace_id', 'revision', 'state', 'request', 'upstream_result', 'caller_result',
    'created_at', 'updated_at', 'truncated',
  ], 'Debug trace');
  const requestRoot = record(root.request, ['route_kind', 'model', 'stream', 'body'], 'Debug request');
  const request: DebugRequest = {
    route_kind: oneOf(requestRoot.route_kind, ['openai_chat_completions', 'charity_chat_completions'] as const, 'Debug route kind'),
    model: string(requestRoot.model, 'Debug model', { max: 512, bytes: 2_048 }),
    stream: boolean(requestRoot.stream, 'Debug stream flag'),
    body: normalizeDebugBody(requestRoot.body),
  };
  let upstream: DebugUpstreamResult | null = null;
  if (root.upstream_result !== null) {
    const item = record(root.upstream_result, ['result_kind', 'status_code', 'upstream_code', 'diag', 'usage', 'completed_at'], 'Debug upstream result');
    upstream = {
      result_kind: oneOf(item.result_kind, ['response', 'synthetic'] as const, 'Debug upstream result kind'),
      status_code: nullableInteger(item.status_code, 'Debug upstream status', 100, 599),
      upstream_code: nullableString(item.upstream_code, 'Debug upstream code', { min: 1, max: 64, bytes: 64, ascii: true }),
      diag: nullableString(item.diag, 'Debug safe diagnostic', { max: 4_096, bytes: 4_096 }),
      usage: normalizeDebugUsage(item.usage),
      completed_at: unixSecond(item.completed_at, 'Debug upstream completion time'),
    };
    if (request.route_kind === 'charity_chat_completions'
      && (upstream.result_kind !== 'synthetic'
        || (upstream.status_code !== 200 && upstream.status_code !== 502)
        || upstream.upstream_code !== null
        || upstream.diag !== null)) {
      invalidResponse('charity Debug upstream projection');
    }
  }
  let caller: DebugCallerResult | null = null;
  if (root.caller_result !== null) {
    const item = record(root.caller_result, ['http_status', 'error_code', 'source', 'message', 'completed_at'], 'Debug caller result');
    const source = oneOf(item.source, ['platform', 'upstream'] as const, 'Debug caller source');
    const message = string(item.message, 'Debug caller message', { min: 1, max: 1_024, bytes: 1_024 });
    const errorCode = item.error_code === null
      ? null
      : oneOf(item.error_code, STABLE_ERROR_CODES, 'Debug caller error code');
    const prefixCount = message.split('[NonbiriAPI] ').length - 1;
    if ((source === 'platform' && (!message.startsWith('[NonbiriAPI] ') || prefixCount !== 1))
      || (source === 'upstream' && prefixCount !== 0)) invalidResponse('Debug caller message source');
    const httpStatus = integer(item.http_status, 'Debug caller status', 100, 599);
    if (errorCode === null) {
      if (!((httpStatus >= 200 && httpStatus <= 399) || httpStatus === 499)) invalidResponse('Debug caller success status');
    } else if (httpStatus < 400
      || (errorCode === 'upstream' ? source !== 'upstream' : source !== 'platform')) {
      invalidResponse('Debug caller error semantics');
    }
    caller = {
      http_status: httpStatus,
      error_code: errorCode,
      source,
      message,
      completed_at: unixSecond(item.completed_at, 'Debug caller completion time'),
    };
  }
  const state = oneOf(root.state, ['capturing', 'terminal'] as const, 'Debug trace state');
  if ((state === 'terminal') !== (caller !== null)) invalidResponse('Debug terminal trace');
  const createdAt = unixSecond(root.created_at, 'Debug trace creation time');
  const updatedAt = unixSecond(root.updated_at, 'Debug trace update time');
  const truncated = boolean(root.truncated, 'Debug trace truncation');
  if (updatedAt < createdAt || truncated !== request.body.truncated) invalidResponse('Debug trace truncation');
  return {
    trace_id: opaqueID(root.trace_id, 'dbt_', 'Debug trace id'),
    revision: decimal(root.revision, 'Debug trace revision', { positive: true }),
    state,
    request,
    upstream_result: upstream,
    caller_result: caller,
    created_at: createdAt,
    updated_at: updatedAt,
    truncated,
  };
}

export function normalizeDebugEvent(value: unknown): DebugEvent {
  const root = record(value, ['version', 'event_id', 'session_id', 'generation', 'kind', 'occurred_at', 'data'], 'Debug event');
  if (root.version !== 2) invalidResponse('Debug event version');
  const base = {
    version: 2 as const,
    event_id: opaqueID(root.event_id, 'dbe_', 'Debug event id'),
    session_id: opaqueID(root.session_id, 'dbs_', 'Debug event session id'),
    generation: decimal(root.generation, 'Debug event generation', { positive: true }),
    occurred_at: unixSecond(root.occurred_at, 'Debug event time'),
  };
  const kind = oneOf(root.kind, ['snapshot', 'trace_upsert', 'gap', 'session_end'] as const, 'Debug event kind');
  if (kind === 'trace_upsert') return { ...base, kind, data: normalizeDebugTrace(root.data) };
  if (kind === 'gap') {
    const data = record(root.data, ['reason', 'first_available_event_id'], 'Debug gap');
    return { ...base, kind, data: {
      reason: oneOf(data.reason, ['cursor_invalid', 'process_restart', 'ring_expired', 'ring_evicted', 'slow_consumer'] as const, 'Debug gap reason'),
      first_available_event_id: data.first_available_event_id === null ? null : opaqueID(data.first_available_event_id, 'dbe_', 'Debug first available event id'),
    } };
  }
  if (kind === 'session_end') {
    const data = record(root.data, ['reason', 'cancelled_inflight_count'], 'Debug session end');
    return { ...base, kind, data: {
      reason: oneOf(data.reason, ['stopped', 'replaced', 'idle_expired', 'absolute_expired', 'auth_revoked', 'account_banned', 'account_deleted', 'shutdown'] as const, 'Debug end reason'),
      cancelled_inflight_count: integer(data.cancelled_inflight_count, 'Debug cancelled inflight count', 0, 32),
    } };
  }
  const data = record(root.data, ['session', 'traces', 'first_event_id', 'last_event_id'], 'Debug snapshot');
  const session = normalizeDebugSession(data.session);
  if (!session.active) invalidResponse('Debug snapshot session');
  const tracesRaw = Array.isArray(data.traces) ? data.traces : invalidResponse('Debug snapshot traces');
  if (tracesRaw.length > 32) invalidResponse('Debug snapshot trace count');
  const traces = tracesRaw.map(normalizeDebugTrace);
  if (new Set(traces.map((trace) => trace.trace_id)).size !== traces.length) invalidResponse('Debug snapshot trace identity');
  const first = data.first_event_id === null ? null : opaqueID(data.first_event_id, 'dbe_', 'Debug first event id');
  const last = data.last_event_id === null ? null : opaqueID(data.last_event_id, 'dbe_', 'Debug snapshot last event id');
  if ((first === null) !== (last === null)) invalidResponse('Debug snapshot event bounds');
  return { ...base, kind, data: { session, traces, first_event_id: first, last_event_id: last } };
}

export function safeRequestJSON(trace: DebugTrace): unknown {
  if (trace.request.body.text === null || !trace.request.body.media_type.toLowerCase().includes('json')) return null;
  try { return JSON.parse(trace.request.body.text) as unknown; } catch { return null; }
}

export type Presence = 'missing' | 'null' | 'false' | 'zero' | 'empty-string' | 'empty-array' | 'empty-object' | 'value';

export function valuePresence(value: unknown, present: boolean): Presence {
  if (!present) return 'missing';
  if (value === null) return 'null';
  if (value === false) return 'false';
  if (value === 0) return 'zero';
  if (value === '') return 'empty-string';
  if (Array.isArray(value) && value.length === 0) return 'empty-array';
  if (value !== null && typeof value === 'object' && !Array.isArray(value) && Object.keys(value).length === 0) return 'empty-object';
  return 'value';
}

export function debugInvalid(error: unknown): ApiError {
  return error instanceof ApiError ? error : new ApiError('invalid_response', 'The Debug service returned an invalid response.', 200);
}
