import { ApiError, apiFetch, type ApiRequestOptions } from '@shared/query/http';
import { record, string, unixSecond } from './wire';

export function operationKey(): string {
  if (!globalThis.crypto?.getRandomValues) {
    throw new ApiError('crypto_unavailable', 'Secure request identity is unavailable.', 0);
  }
  const bytes = new Uint8Array(16);
  globalThis.crypto.getRandomValues(bytes);
  let binary = '';
  for (const byte of bytes) binary += String.fromCharCode(byte);
  const encoded = globalThis.btoa(binary).replaceAll('+', '-').replaceAll('/', '_').replace(/=+$/, '');
  if (!/^[A-Za-z0-9_-]{22}$/.test(encoded)) {
    throw new ApiError('crypto_unavailable', 'Secure request identity is unavailable.', 0);
  }
  return encoded;
}

export function idempotentOptions(
  key: string,
  options: ApiRequestOptions = {},
  elevatedToken?: string,
): ApiRequestOptions {
  const headers = new Headers(options.headers);
  headers.set('Idempotency-Key', key);
  if (elevatedToken) headers.set('X-Elevated-Token', elevatedToken);
  return { ...options, headers };
}

export async function decoded<T>(
  path: string,
  decode: (value: unknown) => T,
  options?: ApiRequestOptions,
): Promise<T> {
  return decode(await apiFetch<unknown>(path, options));
}

export interface AdminElevation {
  token: string;
  expires_at: number;
}

export async function elevateAdmin(password: string): Promise<AdminElevation> {
  const payload = await apiFetch<unknown>('/admin/api/auth/elevate', {
    method: 'POST',
    json: { password },
  });
  const root = record(payload, ['token', 'expires_at'], 'administrator elevation');
  return {
    token: string(root.token, 'administrator elevation token', { min: 22, max: 256, bytes: 256 }),
    expires_at: unixSecond(root.expires_at, 'administrator elevation expiry'),
  };
}

export function queryPath(path: string, values: Record<string, string | number | boolean | null | undefined>): string {
  const query = new URLSearchParams();
  for (const [key, value] of Object.entries(values)) {
    if (value !== undefined && value !== null && value !== '') query.set(key, String(value));
  }
  const encoded = query.toString();
  return encoded ? `${path}?${encoded}` : path;
}

export function conflictOrUnknown(error: unknown): boolean {
  return error instanceof ApiError
    && (error.status === 409 || error.status === 0 || error.status >= 500 || error.code === 'invalid_response');
}

export function responseOutcomeUnknown(error: unknown): boolean {
  return error instanceof ApiError
    && (error.status === 0 || error.status >= 500 || error.code === 'invalid_response');
}
