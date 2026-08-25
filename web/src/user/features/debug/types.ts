/**
 * The user Debug feature owns these wire types instead of putting captured
 * request data in the shared query/data layer. They intentionally model the
 * server debug projection as unknown JSON at the boundary; renderers only use
 * safe, bounded text projections after decoding.
 */

export type DebugMode = 'dry' | 'live';

export type DebugEventType = 'session_snapshot' | 'trace_upsert' | 'gap' | 'session_end';

export type SessionEndReason =
  | 'stopped'
  | 'replaced'
  | 'idle_timeout'
  | 'max_age'
  | 'session_invalid'
  | 'logout'
  | 'banned'
  | 'deleted'
  | 'shutdown'
  | 'capacity';

export interface DebugLimits {
  max_sessions: number;
  hub_bytes: number;
  session_bytes: number;
  max_traces: number;
  max_events: number;
  event_bytes: number;
  subscriber_queue: number;
  max_subscribers: number;
  raw_request_bytes: number;
  messages_tools_bytes: number;
  parameters_bytes: number;
  effective_summary_bytes: number;
  response_bytes: number;
  trace_bytes: number;
  first_attach_seconds: number;
  reconnect_seconds: number;
  idle_seconds: number;
  absolute_seconds: number;
  heartbeat_seconds: number;
  write_deadline_seconds: number;
  confirmation_seconds: number;
}

export interface DebugSessionMetadata {
  id: string;
  generation: number;
  mode: DebugMode;
  created_at: number;
  expires_at: number;
  idle_expires_at: number;
  connected: boolean;
  last_event_id: number;
  limits: DebugLimits;
}

export interface DebugEventEnvelope {
  version: 1;
  seq: number;
  type: DebugEventType;
  session_id: string;
  trace_id: string;
  revision: number;
  at: number;
  payload: Record<string, unknown>;
}

export interface DebugTrace {
  [key: string]: unknown;
}

export interface DebugTraceRecord {
  id: string;
  revision: number;
  payload: DebugTrace;
}

export interface DebugSessionSnapshot {
  metadata: DebugSessionMetadata | null;
  traces: readonly DebugTraceRecord[];
  lastEventId: number;
  dropped: number;
  gapReason: string | null;
  endReason: SessionEndReason | null;
  recoveryRequired: boolean;
  recoveryFatal: boolean;
}

export interface DebugSessionResponseInactive {
  active: false;
}

export type DebugSessionResponse = DebugSessionResponseInactive | DebugSessionMetadata;

export interface DebugErrorPayload {
  error?: {
    code?: unknown;
    message?: unknown;
  };
}

export interface FragmentPayload {
  kind: 'snapshot' | 'trace';
  encoding: 'base64url-json';
  index: number;
  count: number;
  total_bytes: number;
  sha256: string;
  data: string;
}

export const SESSION_END_REASONS: readonly SessionEndReason[] = [
  'stopped',
  'replaced',
  'idle_timeout',
  'max_age',
  'session_invalid',
  'logout',
  'banned',
  'deleted',
  'shutdown',
  'capacity',
] as const;

const EVENT_TYPES: readonly DebugEventType[] = [
  'session_snapshot',
  'trace_upsert',
  'gap',
  'session_end',
] as const;

const LIMIT_KEYS: readonly (keyof DebugLimits)[] = [
  'max_sessions',
  'hub_bytes',
  'session_bytes',
  'max_traces',
  'max_events',
  'event_bytes',
  'subscriber_queue',
  'max_subscribers',
  'raw_request_bytes',
  'messages_tools_bytes',
  'parameters_bytes',
  'effective_summary_bytes',
  'response_bytes',
  'trace_bytes',
  'first_attach_seconds',
  'reconnect_seconds',
  'idle_seconds',
  'absolute_seconds',
  'heartbeat_seconds',
  'write_deadline_seconds',
  'confirmation_seconds',
] as const;

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}

function safeInteger(value: unknown, minimum = 0): value is number {
  return typeof value === 'number' && Number.isSafeInteger(value) && value >= minimum;
}

function safeString(value: unknown, maxBytes = 4096, nonEmpty = false): value is string {
  if (typeof value !== 'string' || (nonEmpty && value.length === 0)) return false;
  if (new TextEncoder().encode(value).byteLength > maxBytes) return false;
  for (const character of value) {
    const code = character.codePointAt(0) ?? 0;
    if (code <= 0x1f || code === 0x7f || (code >= 0xd800 && code <= 0xdfff)) return false;
  }
  return true;
}

const DEBUG_SESSION_ID_PATTERN = /^dbg_[A-Za-z0-9_-]{22}$/;

export function isDebugSessionID(value: unknown): value is string {
  return safeString(value, 26, true) && DEBUG_SESSION_ID_PATTERN.test(value);
}

export function isDebugMode(value: unknown): value is DebugMode {
  return value === 'dry' || value === 'live';
}

export function isSessionEndReason(value: unknown): value is SessionEndReason {
  return typeof value === 'string' && SESSION_END_REASONS.includes(value as SessionEndReason);
}

export function decodeLimits(value: unknown): DebugLimits | null {
  if (!isRecord(value)) return null;
  const decoded = {} as DebugLimits;
  for (const key of LIMIT_KEYS) {
    if (!safeInteger(value[key])) return null;
    decoded[key] = value[key] as number;
  }
  return decoded;
}

export function decodeSessionMetadata(value: unknown): DebugSessionMetadata | null {
  if (!isRecord(value)) return null;
  if (
    !isDebugSessionID(value.id) ||
    !safeInteger(value.generation, 1) ||
    !isDebugMode(value.mode) ||
    !safeInteger(value.created_at) ||
    !safeInteger(value.expires_at) ||
    !safeInteger(value.idle_expires_at) ||
    typeof value.connected !== 'boolean' ||
    !safeInteger(value.last_event_id)
  ) {
    return null;
  }
  if (value.created_at > value.idle_expires_at || value.idle_expires_at > value.expires_at) {
    return null;
  }
  const limits = decodeLimits(value.limits);
  if (!limits) return null;
  return {
    id: value.id,
    generation: value.generation,
    mode: value.mode,
    created_at: value.created_at,
    expires_at: value.expires_at,
    idle_expires_at: value.idle_expires_at,
    connected: value.connected,
    last_event_id: value.last_event_id,
    limits,
  };
}

export function decodeSessionResponse(value: unknown): DebugSessionResponse | null {
  if (isRecord(value) && value.active === false && Object.keys(value).length === 1) {
    return { active: false };
  }
  return decodeSessionMetadata(value);
}

export function decodeEventEnvelope(value: unknown): DebugEventEnvelope | null {
  if (!isRecord(value)) return null;
  const type = value.type;
  if (!EVENT_TYPES.includes(type as DebugEventType)) return null;
  if (
    value.version !== 1 ||
    !safeInteger(value.seq, 1) ||
    !isDebugSessionID(value.session_id) ||
    !safeInteger(value.revision) ||
    !safeInteger(value.at) ||
    !isRecord(value.payload)
  ) {
    return null;
  }
  if (type === 'trace_upsert') {
    if (!safeString(value.trace_id, 128, true) || value.revision < 1) return null;
  } else if (value.trace_id !== '' || value.revision !== 0) {
    return null;
  }
  return {
    version: 1,
    seq: value.seq,
    type: type as DebugEventType,
    session_id: value.session_id,
    trace_id: value.trace_id,
    revision: value.revision,
    at: value.at,
    payload: value.payload,
  };
}

export function decodeFragment(value: unknown): FragmentPayload | null {
  if (!isRecord(value)) return null;
  if (
    (value.kind !== 'snapshot' && value.kind !== 'trace') ||
    value.encoding !== 'base64url-json' ||
    !safeInteger(value.index) ||
    !safeInteger(value.count, 1) ||
    value.index >= value.count ||
    !safeInteger(value.total_bytes, 1) ||
    !safeString(value.sha256, 64, true) ||
    !/^[0-9a-f]{64}$/.test(value.sha256) ||
    !safeString(value.data, 512 * 1024, true)
  ) {
    return null;
  }
  return {
    kind: value.kind,
    encoding: 'base64url-json',
    index: value.index,
    count: value.count,
    total_bytes: value.total_bytes,
    sha256: value.sha256,
    data: value.data,
  };
}

export function isRecordValue(value: unknown): value is Record<string, unknown> {
  return isRecord(value);
}
