export const FAILURE_CATEGORIES = [
  'network',
  'policy',
  'tls',
  'timeout',
  'upstream',
  'protocol',
  'encoding',
  'resource_limit',
  'canceled',
  'delivery',
  'platform',
  'unknown',
] as const;

export const FAILURE_PHASES = [
  'dispatch',
  'dns',
  'connect',
  'tls',
  'send_request',
  'wait_headers',
  'response_headers',
  'queue',
  'policy',
  'redirect',
  'credential',
  'upstream_response',
  'read_response',
  'decode_response',
  'parse_sse',
  'convert_response',
  'write_client',
  'execute',
  'transport',
] as const;

export interface FailureCause {
  type?: string;
  code?: string;
  message?: string;
}
export interface FailureDiagnostics {
  category?: string;
  phase?: string;
  code?: string;
  message?: string;
  causes: FailureCause[];
  secondary: FailureCause[];
  correlation: { source: string; name: string; value: string }[];
  timings: { phase: string; elapsed_ms: number }[];
  started_at_ms?: number;
  failed_at_ms?: number;
  completed_at_ms?: number;
  first_body_at_ms?: number;
  upstream_status?: number;
  headers_received?: boolean;
  received_bytes?: number;
  received_events?: number;
  forwarded_bytes?: number;
  response_started?: boolean;
  body_eof?: boolean;
  protocol_complete?: boolean;
  usage_present?: boolean;
  content_type?: string;
  content_encoding?: string;
  budget_ms?: number;
  retry_after_seconds?: number;
  limit_bytes?: number;
  limit_kind?: string;
  incomplete: boolean;
}
export interface DeliveryDiagnostics {
  http_status?: number;
  bytes?: number;
  failed?: boolean;
  failure?: FailureDiagnostics | null;
  incomplete: boolean;
}

function object(value: unknown): Record<string, unknown> | undefined {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : undefined;
}
function text(value: unknown, maximum = 512): string | undefined {
  return typeof value === 'string' && value.length > 0 ? value.slice(0, maximum) : undefined;
}
function count(value: unknown): number | undefined {
  return Number.isSafeInteger(value) && (value as number) >= 0 ? (value as number) : undefined;
}
function status(value: unknown): number | undefined {
  const parsed = count(value);
  return parsed !== undefined && parsed >= 100 && parsed <= 599 ? parsed : undefined;
}

// A damaged observation is local to that field; it must not hide the request,
// accounting result, or other independently readable observations.
export function normalizeFailure(value: unknown): FailureDiagnostics | null | undefined {
  if (value == null) return undefined;
  const raw = object(value);
  if (!raw) return null;
  const result: FailureDiagnostics = {
    causes: [],
    secondary: [],
    correlation: [],
    timings: [],
    incomplete: false,
  };
  for (const key of [
    'category',
    'phase',
    'code',
    'message',
    'content_type',
    'content_encoding',
    'limit_kind',
  ] as const) {
    const parsed = text(raw[key]);
    if (parsed !== undefined) result[key] = parsed;
    else if (raw[key] != null) result.incomplete = true;
  }
  for (const key of [
    'started_at_ms',
    'failed_at_ms',
    'completed_at_ms',
    'first_body_at_ms',
    'received_bytes',
    'received_events',
    'forwarded_bytes',
    'budget_ms',
    'retry_after_seconds',
    'limit_bytes',
  ] as const) {
    const parsed = count(raw[key]);
    if (parsed !== undefined) result[key] = parsed;
    else if (raw[key] != null) result.incomplete = true;
  }
  result.upstream_status = status(raw.upstream_status);
  if (raw.upstream_status != null && result.upstream_status === undefined) result.incomplete = true;
  for (const key of [
    'headers_received',
    'response_started',
    'body_eof',
    'protocol_complete',
    'usage_present',
  ] as const) {
    if (typeof raw[key] === 'boolean') result[key] = raw[key];
    else if (raw[key] != null) result.incomplete = true;
  }
  function entries<T>(key: string, maximum: number, parse: (item: unknown) => T | undefined): T[] {
    const values = raw?.[key];
    if (values == null) return [];
    if (!Array.isArray(values)) {
      result.incomplete = true;
      return [];
    }
    if (values.length > maximum) result.incomplete = true;
    return values.slice(0, maximum).flatMap((item) => {
      const parsed = parse(item);
      if (parsed !== undefined) return [parsed];
      result.incomplete = true;
      return [];
    });
  }
  for (const key of ['causes', 'secondary'] as const) {
    result[key] = entries(key, key === 'causes' ? 8 : 4, (value) => {
      const item = object(value);
      if (!item) return;
      const cause = {
        type: text(item.type, 128),
        code: text(item.code, 128),
        message: text(item.message),
      };
      return cause.type || cause.code || cause.message ? cause : undefined;
    });
  }
  result.correlation = entries('correlation', 8, (value) => {
    const item = object(value);
    if (!item) return;
    const source = text(item.source, 64),
      name = text(item.name, 64),
      id = text(item.value, 256);
    return source && name && id ? { source, name, value: id } : undefined;
  });
  result.timings = entries('timings', 12, (value) => {
    const item = object(value);
    if (!item) return;
    const phase = text(item.phase, 64),
      elapsed = count(item.elapsed_ms);
    return phase && elapsed !== undefined ? { phase, elapsed_ms: elapsed } : undefined;
  });
  return result;
}

export function normalizeDelivery(value: unknown): DeliveryDiagnostics | null | undefined {
  if (value == null) return undefined;
  const raw = object(value);
  if (!raw) return null;
  const result: DeliveryDiagnostics = {
    http_status: status(raw.http_status),
    bytes: count(raw.bytes),
    failed: typeof raw.failed === 'boolean' ? raw.failed : undefined,
    failure: normalizeFailure(raw.failure),
    incomplete: false,
  };
  result.incomplete =
    (raw.http_status != null && result.http_status === undefined) ||
    (raw.bytes != null && result.bytes === undefined) ||
    (raw.failed != null && result.failed === undefined) ||
    result.failure === null;
  return result;
}

export function failureCategoryKey(category?: string): string {
  return FAILURE_CATEGORIES.some((item) => item === category)
    ? 'common.failure.category.' + category
    : 'common.failure.category.unknown';
}
export function failurePhaseKey(phase?: string): string {
  return FAILURE_PHASES.some((item) => item === phase)
    ? 'common.failure.phase.' + phase
    : phase
      ? 'common.failure.unknownPhase'
      : 'common.failure.notRecorded';
}
