export const FAILURE_CATEGORIES = [
  'configuration',
  'connection',
  'unavailable',
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
  'configuration',
  'decode_catalogue',
  'local_usage',
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

const GOOGLE_MODEL_QUOTA_REASON_KEYS = {
  google_daily_requests: 'common.failure.reason.google_daily_requests',
  google_minute_input_tokens: 'common.failure.reason.google_minute_input_tokens',
  google_minute_requests: 'common.failure.reason.google_minute_requests',
  google_daily_input_tokens: 'common.failure.reason.google_daily_input_tokens',
  google_model_quota: 'common.failure.reason.google_model_quota',
};

const PROVIDER_REASON_KEYS = {
  ...GOOGLE_MODEL_QUOTA_REASON_KEYS,
  google_access_token_type_unsupported:
    'common.failure.reason.google_access_token_type_unsupported',
  google_api_key_invalid: 'common.failure.reason.google_api_key_invalid',
  google_model_overloaded: 'common.failure.reason.google_model_overloaded',
};

const FAILURE_REASON_KEYS: Record<string, string> = {
  ...PROVIDER_REASON_KEYS,
  http_error: 'common.failure.reason.http_error',
  attempt_failed: 'common.failure.reason.attempt_failed',
  dns_timeout: 'common.failure.reason.dns_timeout',
  dns_not_found: 'common.failure.reason.dns_not_found',
  dns_failed: 'common.failure.reason.dns_failed',
  tls_unknown_authority: 'common.failure.reason.tls_unknown_authority',
  tls_hostname_mismatch: 'common.failure.reason.tls_hostname_mismatch',
  tls_invalid_certificate: 'common.failure.reason.tls_invalid_certificate',
  tls_record_invalid: 'common.failure.reason.tls_record_invalid',
  canceled: 'common.failure.reason.canceled',
  deadline_exceeded: 'common.failure.reason.deadline_exceeded',
  network_timeout: 'common.failure.reason.network_timeout',
  compression_header_invalid: 'common.failure.reason.compression_header_invalid',
  compression_checksum_invalid: 'common.failure.reason.compression_checksum_invalid',
  unexpected_eof: 'common.failure.reason.unexpected_eof',
  connection_reset: 'common.failure.reason.connection_reset',
  connection_refused: 'common.failure.reason.connection_refused',
  connection_aborted: 'common.failure.reason.connection_aborted',
  connection_closed: 'common.failure.reason.connection_closed',
  broken_pipe: 'common.failure.reason.broken_pipe',
  connect_failed: 'common.failure.reason.connect_failed',
  read_failed: 'common.failure.reason.read_failed',
  write_failed: 'common.failure.reason.write_failed',
  invalid_json: 'common.failure.reason.invalid_json',
  json_type_mismatch: 'common.failure.reason.json_type_mismatch',
  invalid_response: 'common.failure.reason.invalid_response',
  client_write_failed: 'common.failure.reason.client_write_failed',
  response_limit: 'common.failure.reason.response_limit',
  upstream_error: 'common.failure.reason.upstream_error',
  network_error: 'common.failure.reason.network_error',
  platform: 'common.failure.reason.platform',
  recovery_unknown: 'common.failure.reason.recovery_unknown',
  legacy_unknown: 'common.failure.reason.legacy_unknown',
};

export function failureReasonKey(code: string | undefined): string | undefined {
  return code && Object.hasOwn(FAILURE_REASON_KEYS, code) ? FAILURE_REASON_KEYS[code] : undefined;
}

export function isGoogleModelQuotaReason(code: string): boolean {
  return Object.hasOwn(GOOGLE_MODEL_QUOTA_REASON_KEYS, code);
}

export interface FailureCause {
  type?: string;
  code?: string;
  message?: string;
}
export interface FailureDiagnostics {
  category?: string;
  phase?: string;
  code?: string;
  provider_reason?: string;
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
export function normalizeFailure(
  value: unknown,
  { includeProviderReason = false } = {},
): FailureDiagnostics | null | undefined {
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
  if (
    includeProviderReason &&
    typeof raw.provider_reason === 'string' &&
    Object.hasOwn(PROVIDER_REASON_KEYS, raw.provider_reason)
  ) {
    result.provider_reason = raw.provider_reason;
  }
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
    ? `common.failure.category.${category}`
    : 'common.failure.category.unknown';
}
export function failurePhaseKey(phase?: string): string {
  return FAILURE_PHASES.some((item) => item === phase)
    ? `common.failure.phase.${phase}`
    : phase
      ? 'common.failure.unknownPhase'
      : 'common.failure.notRecorded';
}
