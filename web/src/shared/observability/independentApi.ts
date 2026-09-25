import { apiFetch } from '@shared/query/http';
import {
  errorBytes,
  type DiagnosticRole,
  type ErrorBody,
  type ErrorMetadata,
  type SourceFacts,
} from './api';
export type IndependentKind = 'all' | 'model_discovery' | 'image_task' | 'image_discovery';
export interface IndependentItem extends ErrorMetadata {
  id: string;
  kind: Exclude<IndependentKind, 'all'>;
  subject_id: string;
  user_id: string;
  attempt_seq: number;
  synthetic: boolean;
  dispatch?: DiagnosticDispatch;
}
export interface DiagnosticDispatch {
  method: 'GET';
  url: string;
  request_body: string;
  content_type: string;
  dispatched_at: number;
}
export interface IndependentPage {
  data: IndependentItem[];
  next_before: string | null;
  from: number;
  to: number;
}
export interface IndependentDetail {
  item: IndependentItem;
  body: ErrorBody;
  source: SourceFacts | null;
}
export interface IndependentFilter {
  kind: IndependentKind;
  user_id?: string;
  subject_id?: string;
  from?: number;
  to?: number;
  before?: string;
}
export const syntheticImageMIME = 'application/vnd.nonbiriapi.image-diagnostic+json';
function invalid(): never {
  throw new Error('Invalid diagnostic response');
}
function object(value: unknown): Record<string, unknown> {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) invalid();
  return value as Record<string, unknown>;
}
function integer(value: unknown, max = 253402300799): number {
  if (typeof value !== 'number' || !Number.isSafeInteger(value) || value < 0 || value > max)
    invalid();
  return value;
}
function positiveID(value: unknown): string {
  if (
    typeof value !== 'string' ||
    !/^[1-9][0-9]{0,18}$/.test(value) ||
    BigInt(value) > 9223372036854775807n
  )
    invalid();
  return value;
}
function hasControl(value: string): boolean {
  for (const character of value) {
    const code = character.codePointAt(0)!;
    if (code < 0x20 || (code >= 0x7f && code <= 0x9f)) return true;
  }
  return false;
}
function metadata(value: unknown): ErrorMetadata {
  const v = object(value);
  if (
    typeof v.content_type !== 'string' ||
    v.content_type.length > 256 ||
    typeof v.truncated !== 'boolean' ||
    !['saved', 'capacity_exhausted', 'unavailable'].includes(String(v.save_state))
  )
    invalid();
  const event = integer(v.event_seq, Number.MAX_SAFE_INTEGER);
  if (event < 1) invalid();
  const status = v.http_status === null ? null : integer(v.http_status, 599);
  if (status !== null && status < 100) invalid();
  const bytes = integer(v.bytes_saved, 1048576);
  if (v.save_state !== 'saved' && bytes !== 0) invalid();
  const created = integer(v.created_at);
  const expires = integer(v.expires_at);
  if (expires < created) invalid();
  return {
    event_seq: event,
    http_status: status,
    content_type: v.content_type,
    bytes_saved: bytes,
    truncated: v.truncated,
    save_state: v.save_state as ErrorMetadata['save_state'],
    created_at: created,
    expires_at: expires,
  };
}
function item(value: unknown): IndependentItem {
  const v = object(value);
  const prefix =
    v.kind === 'model_discovery'
      ? 'req_'
      : v.kind === 'image_task'
        ? 'img_'
        : v.kind === 'image_discovery'
          ? 'op_'
          : '';
  if (
    !prefix ||
    typeof v.subject_id !== 'string' ||
    !new RegExp(`^${prefix}[A-Za-z0-9_-]{21}[AQgw]$`).test(v.subject_id) ||
    typeof v.synthetic !== 'boolean'
  )
    invalid();
  const meta = metadata(v);
  if (v.synthetic !== (meta.content_type === syntheticImageMIME)) invalid();
  const attempt = integer(v.attempt_seq, 2147483647);
  if (attempt < 1) invalid();
  let dispatch: DiagnosticDispatch | undefined;
  if (v.dispatch !== undefined) {
    if (v.kind !== 'image_discovery') invalid();
    const d = object(v.dispatch);
    if (
      d.method !== 'GET' ||
      typeof d.url !== 'string' ||
      d.url.length < 1 ||
      d.url.length > 8192 ||
      hasControl(d.url) ||
      typeof d.request_body !== 'string' ||
      d.request_body !== '' ||
      typeof d.content_type !== 'string' ||
      d.content_type !== '' ||
      typeof d.dispatched_at !== 'number' ||
      !Number.isSafeInteger(d.dispatched_at) ||
      d.dispatched_at < 0
    )
      invalid();
    dispatch = {
      method: 'GET',
      url: d.url,
      request_body: '',
      content_type: '',
      dispatched_at: d.dispatched_at,
    };
  }
  return {
    ...meta,
    id: positiveID(v.id),
    user_id: positiveID(v.user_id),
    kind: v.kind as IndependentItem['kind'],
    subject_id: v.subject_id,
    attempt_seq: attempt,
    synthetic: v.synthetic,
    ...(dispatch ? { dispatch } : {}),
  };
}
export function decodeIndependentPage(value: unknown): IndependentPage {
  const v = object(value);
  if (!Array.isArray(v.data) || v.data.length > 20) invalid();
  const from = integer(v.from);
  const to = integer(v.to);
  if (to <= from || to - from > 30 * 86400) invalid();
  const data = v.data.map(item);
  if (data.some((entry, index) => index > 0 && BigInt(entry.id) >= BigInt(data[index - 1].id)))
    invalid();
  const next = v.next_before === null ? null : positiveID(v.next_before);
  if (next !== null && (data.length === 0 || next !== data.at(-1)?.id)) invalid();
  return { data, from, to, next_before: next };
}
function source(value: unknown): SourceFacts | null {
  if (value === null) return null;
  const v = object(value);
  const keys = [
    'effective_ip',
    'ip_quality',
    'user_agent',
    'origin',
    'referer',
    'http_referer',
    'openrouter_title',
    'legacy_title',
    'sdk_lang',
    'sdk_version',
    'sdk_runtime',
    'sdk_runtime_version',
    'quality',
  ];
  if (
    Object.keys(v).some((key) => !keys.includes(key)) ||
    JSON.stringify(v).length > 8192 ||
    typeof v.effective_ip !== 'string' ||
    !['direct_peer', 'trusted_forwarded', 'peer_fallback'].includes(String(v.ip_quality))
  )
    invalid();
  for (const [key, entry] of Object.entries(v)) {
    if (key === 'quality') {
      for (const [field, flags] of Object.entries(object(entry))) {
        if (!keys.includes(field) || field === 'quality') invalid();
        for (const [flag, enabled] of Object.entries(object(flags))) {
          if (!['truncated', 'multiple', 'invalid'].includes(flag) || typeof enabled !== 'boolean')
            invalid();
        }
      }
    } else if (typeof entry !== 'string') invalid();
  }
  return v as unknown as SourceFacts;
}
export function decodeIndependentDetail(value: unknown): IndependentDetail {
  const v = object(value);
  const entry = item(v.item);
  const raw = object(v.body);
  const meta = metadata(raw);
  for (const [key, field] of Object.entries(meta))
    if (field !== entry[key as keyof ErrorMetadata]) invalid();
  if ((raw.encoding !== 'utf-8' && raw.encoding !== 'base64') || typeof raw.body !== 'string')
    invalid();
  const body: ErrorBody = { ...meta, encoding: raw.encoding, body: raw.body };
  errorBytes(body);
  return { item: entry, body, source: source(v.source) };
}
function root(role: DiagnosticRole) {
  if (role !== 'admin' && role !== 'steward') invalid();
  return role === 'admin' ? '/admin/api/diagnostics' : '/api/steward/diagnostics';
}
export async function getIndependentDiagnostics(
  role: DiagnosticRole,
  filter: IndependentFilter,
  signal?: AbortSignal,
) {
  const query = new URLSearchParams({ kind: filter.kind, page_size: '20' });
  for (const [key, value] of Object.entries(filter))
    if (key !== 'kind' && value !== undefined && value !== '') query.set(key, String(value));
  return decodeIndependentPage(await apiFetch<unknown>(`${root(role)}?${query}`, { signal }));
}
export async function getIndependentDiagnostic(
  role: DiagnosticRole,
  id: string,
  signal?: AbortSignal,
) {
  return decodeIndependentDetail(
    await apiFetch<unknown>(`${root(role)}/${positiveID(id)}`, { signal }),
  );
}
