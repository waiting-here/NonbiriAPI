import { captureMfaRequiredHandlers } from '@shared/query/mfaRequired';
import { requestLanguage } from '@shared/query/language';
import { ApiError, isApiError } from '@shared/query/http';
import { validateIdempotencyKey } from './normalizers';
import type { OperationIdentity } from './types';

interface CoreRequestOptions extends Omit<RequestInit, 'body'> {
  json?: unknown;
}

export interface CoreResponse {
  status: number;
  headers: Headers;
  payload: unknown;
}

function fallbackCode(status: number): string {
  if (status === 400) return 'invalid_request';
  if (status === 401) return 'unauthorized';
  if (status === 403) return 'forbidden';
  if (status === 404) return 'not_found';
  if (status === 409) return 'conflict';
  if (status === 423) return 'resource_locked';
  if (status === 429) return 'rate_limited';
  if (status === 503) return 'service_unavailable';
  if (status >= 500) return 'internal';
  return 'http_error';
}

function fallbackMessage(status: number): string {
  if (status === 401) return 'Authentication is required.';
  if (status === 403) return 'This action is not allowed.';
  if (status === 404) return 'The requested resource was not found.';
  if (status === 409) return 'The resource changed. Refresh and review it again.';
  if (status === 423) return 'The resource is currently locked.';
  if (status === 429) return 'Too many requests. Try again later.';
  if (status >= 500) return 'The service could not complete the request.';
  return 'The request could not be completed.';
}

async function apiError(response: Response): Promise<ApiError> {
  let code: string | undefined;
  let message: string | undefined;
  try {
    const payload = (await response.json()) as unknown;
    if (payload !== null && typeof payload === 'object' && !Array.isArray(payload)) {
      const error = (payload as Record<string, unknown>).error;
      if (error !== null && typeof error === 'object' && !Array.isArray(error)) {
        const detail = error as Record<string, unknown>;
        code = typeof detail.code === 'string' ? detail.code : undefined;
        message = typeof detail.message === 'string' ? detail.message : undefined;
      }
    }
  } catch {
    // Proxy HTML and malformed JSON stay outside the UI boundary.
  }
  return new ApiError(
    code ?? fallbackCode(response.status),
    message ?? fallbackMessage(response.status),
    response.status,
  );
}

export async function coreRawRequest(
  path: string,
  options: CoreRequestOptions = {},
): Promise<Response> {
  const mfaHandlers = path.startsWith('/api/') ? captureMfaRequiredHandlers() : [];
  const { json, headers, ...init } = options;
  const requestHeaders = new Headers(headers);
  requestHeaders.set('Accept', 'application/json');
  requestHeaders.set('Accept-Language', requestLanguage());
  requestHeaders.set('Cache-Control', 'no-store');
  if (json !== undefined) requestHeaders.set('Content-Type', 'application/json');

  let response: Response;
  try {
    response = await fetch(path, {
      ...init,
      body: json === undefined ? undefined : JSON.stringify(json),
      cache: 'no-store',
      credentials: 'same-origin',
      headers: requestHeaders,
    });
  } catch (error) {
    if (init.signal?.aborted) throw error;
    throw new ApiError('network_error', 'The network request failed.', 0);
  }
  if (!response.ok) {
    const error = await apiError(response);
    if (error.code === 'mfa_required') for (const notify of mfaHandlers) notify();
    throw error;
  }
  return response;
}

export async function coreRequest(
  path: string,
  options: CoreRequestOptions = {},
): Promise<CoreResponse> {
  const response = await coreRawRequest(path, options);
  if (response.status === 204)
    return { status: response.status, headers: response.headers, payload: undefined };
  let payload: unknown;
  try {
    payload = await response.json();
  } catch {
    throw new ApiError(
      'invalid_response',
      'The server returned an invalid response.',
      response.status,
    );
  }
  return { status: response.status, headers: response.headers, payload };
}

function randomToken(): string {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    return crypto.randomUUID();
  }
  if (typeof crypto !== 'undefined' && typeof crypto.getRandomValues === 'function') {
    const bytes = crypto.getRandomValues(new Uint8Array(24));
    return Array.from(bytes, (byte) => byte.toString(16).padStart(2, '0')).join('');
  }
  throw new ApiError(
    'operation_identity_unavailable',
    'A secure operation identity could not be created.',
    0,
  );
}

export function createOperationIdentity(): OperationIdentity {
  const idempotencyKey = validateIdempotencyKey(randomToken());
  return { idempotencyKey, actionId: randomToken() };
}

export function operationHeaders(operation: OperationIdentity): HeadersInit {
  return { 'Idempotency-Key': validateIdempotencyKey(operation.idempotencyKey) };
}

export function isConflict(error: unknown): boolean {
  return isApiError(error) && (error.status === 409 || error.code === 'conflict');
}

export function isOutcomeUnknown(error: unknown): boolean {
  return (
    isApiError(error) &&
    (error.status === 0 ||
      error.status >= 500 ||
      error.code === 'invalid_response' ||
      error.code === 'network_error')
  );
}

export function callerSafeError(error: unknown): string | undefined {
  return isApiError(error) ? error.message : undefined;
}
