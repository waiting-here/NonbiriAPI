import { ApiError } from '@shared/query/http';
import { encodeBase64URL } from './strict';

const MAX_RESPONSE_BYTES = 256 * 1024;

export interface GameResponse<T> {
  readonly status: number;
  readonly data: T;
}

export interface GameRequestOptions {
  readonly method?: 'GET' | 'POST' | 'DELETE';
  readonly json?: unknown;
  readonly idempotencyKey?: string;
  readonly signal?: AbortSignal;
  readonly expectedStatuses?: readonly number[];
}

function boundedError(value: unknown, fallback: string): string {
  if (typeof value !== 'string' || value.length === 0) return fallback;
  return Array.from(value).slice(0, 1000).join('');
}

function errorFrom(status: number, body: unknown): ApiError {
  const root =
    body !== null && typeof body === 'object' && !Array.isArray(body)
      ? (body as Record<string, unknown>)
      : null;
  const raw = root?.error;
  const detail =
    raw !== null && typeof raw === 'object' && !Array.isArray(raw)
      ? (raw as Record<string, unknown>)
      : null;
  const code =
    typeof detail?.code === 'string' ? detail.code : status === 409 ? 'conflict' : `http_${status}`;
  return new ApiError(
    code,
    boundedError(detail?.message, `Game request failed (HTTP ${status}).`),
    status,
  );
}

async function responseJSON(response: Response): Promise<unknown> {
  const declaredLength = response.headers.get('Content-Length');
  if (declaredLength && /^\d+$/.test(declaredLength) && Number(declaredLength) > MAX_RESPONSE_BYTES)
    throw new ApiError(
      'invalid_response',
      'The game response exceeded its safe limit.',
      response.status,
    );
  if (!response.body) return undefined;
  const reader = response.body.getReader();
  const chunks: Uint8Array[] = [];
  let total = 0;
  try {
    while (true) {
      const { done, value } = await reader.read();
      if (done) break;
      total += value.byteLength;
      if (total > MAX_RESPONSE_BYTES) {
        await reader.cancel().catch(() => undefined);
        throw new ApiError(
          'invalid_response',
          'The game response exceeded its safe limit.',
          response.status,
        );
      }
      chunks.push(value);
    }
  } finally {
    reader.releaseLock();
  }
  const bytes = new Uint8Array(total);
  let offset = 0;
  for (const chunk of chunks) {
    bytes.set(chunk, offset);
    offset += chunk.byteLength;
  }
  let body: string;
  try {
    body = new TextDecoder('utf-8', { fatal: true }).decode(bytes);
  } catch {
    throw new ApiError(
      'invalid_response',
      'The server returned malformed game data.',
      response.status,
    );
  }
  if (body.length === 0) return undefined;
  try {
    return JSON.parse(body) as unknown;
  } catch {
    throw new ApiError(
      'invalid_response',
      'The server returned malformed game data.',
      response.status,
    );
  }
}

export async function gameRequest<T>(
  path: string,
  options: GameRequestOptions = {},
): Promise<GameResponse<T>> {
  const headers = new Headers({ Accept: 'application/json', 'Cache-Control': 'no-store' });
  if (options.json !== undefined) headers.set('Content-Type', 'application/json');
  if (options.idempotencyKey) headers.set('Idempotency-Key', options.idempotencyKey);
  let response: Response;
  try {
    response = await fetch(path, {
      method: options.method ?? 'GET',
      body: options.json === undefined ? undefined : JSON.stringify(options.json),
      cache: 'no-store',
      credentials: 'same-origin',
      headers,
      signal: options.signal,
    });
  } catch (error) {
    if (error instanceof DOMException && error.name === 'AbortError') throw error;
    throw new ApiError('network_error', 'The game request response is unknown.', 0);
  }
  const body = await responseJSON(response);
  if (!response.ok) throw errorFrom(response.status, body);
  if (options.expectedStatuses && !options.expectedStatuses.includes(response.status)) {
    throw new ApiError(
      'invalid_response',
      'The server returned an unexpected game status.',
      response.status,
    );
  }
  return { status: response.status, data: body as T };
}

export function secureBytes(length: number): Uint8Array {
  if (!globalThis.crypto?.getRandomValues)
    throw new Error('Secure random generation is unavailable.');
  const bytes = new Uint8Array(length);
  globalThis.crypto.getRandomValues(bytes);
  return bytes;
}

export function createIdempotencyKey(): string {
  return encodeBase64URL(secureBytes(24));
}

export function createOpaqueID(prefix: string): string {
  return `${prefix}${encodeBase64URL(secureBytes(16))}`;
}

export function isConflict(error: unknown): boolean {
  return error instanceof ApiError && (error.status === 409 || error.code === 'conflict');
}

export function isMaintenance(error: unknown): boolean {
  return error instanceof ApiError && error.code === 'maintenance';
}

export function isServiceStopped(error: unknown): boolean {
  return error instanceof ApiError && error.code === 'service_unavailable';
}

export function isResponseUnknown(error: unknown): boolean {
  return (
    error instanceof ApiError &&
    (error.status === 0 ||
      error.code === 'network_error' ||
      error.code === 'invalid_response' ||
      error.status >= 500)
  );
}
