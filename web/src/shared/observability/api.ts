import { apiFetch } from '@shared/query/http';

export type DiagnosticRole = 'admin' | 'steward';
export interface SourceFacts {
  effective_ip: string;
  ip_quality: 'direct_peer' | 'trusted_forwarded' | 'peer_fallback';
  user_agent?: string;
  origin?: string;
  referer?: string;
  http_referer?: string;
  openrouter_title?: string;
  legacy_title?: string;
  sdk_lang?: string;
  sdk_version?: string;
  sdk_runtime?: string;
  sdk_runtime_version?: string;
  quality?: Record<string, { truncated?: boolean; multiple?: boolean; invalid?: boolean }>;
}
export interface ErrorMetadata {
  event_seq: number;
  http_status: number | null;
  content_type: string;
  bytes_saved: number;
  truncated: boolean;
  save_state: 'saved' | 'capacity_exhausted' | 'unavailable';
  created_at: number;
  expires_at: number;
}
export interface ErrorBody extends ErrorMetadata {
  encoding: 'utf-8' | 'base64';
  body: string;
}
export interface ErrorPage {
  data: ErrorMetadata[];
  next_after: number | null;
}
export interface RecentSuccess {
  window_start: number;
  as_of: number;
  success: number;
  failure: number;
  cancelled: number;
  sample_count: number;
  rate: number | null;
  insufficient_sample: boolean;
  capture_started_at: number;
}

function root(role: DiagnosticRole, requestID: string) {
  if (!/^req_[A-Za-z0-9_-]{21}[AQgw]$/.test(requestID)) throw new Error('Invalid request ID');
  return `${role === 'admin' ? '/admin/api' : '/api/steward'}/logs/${requestID}`;
}
export function getSource(role: DiagnosticRole, requestID: string, signal?: AbortSignal) {
  return apiFetch<SourceFacts | null>(`${root(role, requestID)}/source`, { signal });
}
export function getErrorList(
  role: DiagnosticRole,
  requestID: string,
  attempt: number,
  after = 0,
  signal?: AbortSignal,
) {
  return apiFetch<ErrorPage>(`${root(role, requestID)}/attempts/${attempt}/errors?after=${after}`, {
    signal,
  });
}
export function getErrorBody(
  role: DiagnosticRole,
  requestID: string,
  attempt: number,
  event: number,
  signal?: AbortSignal,
) {
  return apiFetch<ErrorBody>(`${root(role, requestID)}/attempts/${attempt}/errors/${event}`, {
    signal,
  });
}

export function errorBytes(body: ErrorBody): Uint8Array<ArrayBuffer> {
  if (body.body.length > 1_398_104 || body.bytes_saved > 1_048_576)
    throw new Error('Diagnostic is too large');
  let bytes: Uint8Array<ArrayBuffer>;
  if (body.encoding === 'utf-8') bytes = new TextEncoder().encode(body.body);
  else {
    const binary = atob(body.body);
    bytes = new Uint8Array(binary.length);
    for (let index = 0; index < binary.length; index++) bytes[index] = binary.charCodeAt(index);
  }
  if (bytes.byteLength !== body.bytes_saved || bytes.byteLength > 1_048_576) throw new Error('Invalid diagnostic byte count');
  return bytes;
}
