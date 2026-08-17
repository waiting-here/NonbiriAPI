import { cleanText } from './normalize';

/**
 * The single HTTP boundary used by both stations.
 *
 * Session authentication is cookie based. API responses are deliberately not
 * persisted by the browser, and one-time secrets are kept in component
 * memory only by the pages that explicitly request them.
 */

export interface ApiErrorBody {
  code: string;
  message: string;
  diag?: string;
  request_id?: string;
}

function boundedText(value: unknown, maxCharacters: number, maxBytes?: number): string | undefined {
  if (typeof value !== 'string') return undefined;
  const normalized = cleanText(value);
  if (!normalized) return undefined;
  const characters = Array.from(normalized);
  let bounded = characters.length > maxCharacters ? characters.slice(0, maxCharacters).join('') : normalized;
  const byteLimit = maxBytes ?? Number.POSITIVE_INFINITY;
  if (byteLimit < Number.POSITIVE_INFINITY) {
    const encoder = new TextEncoder();
    let bytes = 0;
    let prefix = '';
    for (const character of Array.from(bounded)) {
      const characterBytes = encoder.encode(character).byteLength;
      const suffixBytes = encoder.encode('…').byteLength;
      if (bytes + characterBytes + suffixBytes > byteLimit) break;
      prefix += character;
      bytes += characterBytes;
    }
    if (prefix.length < bounded.length || bounded.length < characters.length) bounded = `${prefix}…`;
    else bounded = prefix;
  } else if (characters.length > maxCharacters) {
    bounded = `${bounded}…`;
  }
  return bounded || undefined;
}

export class ApiError extends Error {
  readonly code: string;
  readonly status: number;
  readonly diag?: string;
  readonly requestId?: string;

  constructor(
    code: string,
    message: string,
    status: number,
    options: { diag?: string; requestId?: string } = {},
  ) {
    const safeMessage = boundedText(message, 1000) ?? 'The request could not be completed.';
    super(safeMessage);
    this.name = 'ApiError';
    this.code = boundedText(code, 96) ?? 'http_error';
    this.status = Number.isFinite(status) && status >= 0 ? Math.trunc(status) : 0;
    this.diag = boundedText(options.diag, 4096, 4096);
    this.requestId = boundedText(options.requestId, 128);
  }
}

export interface ApiRequestOptions extends RequestInit {
  /** Object payload, serialized to JSON automatically. */
  json?: unknown;
}

function parseEnvelope(body: unknown): ApiErrorBody | null {
  if (body === null || typeof body !== 'object' || Array.isArray(body)) return null;
  const root = body as Record<string, unknown>;
  const rawError = root.error;
  if (rawError === null || typeof rawError !== 'object' || Array.isArray(rawError)) return null;

  const error = rawError as Record<string, unknown>;
  const code = boundedText(error.code, 96);
  const message = boundedText(error.message, 1000);
  if (!code || !message) return null;

  const diag = boundedText(error.diag, 4096, 4096);
  const requestId = boundedText(error.request_id, 128);
  return {
    code,
    message,
    ...(diag ? { diag } : {}),
    ...(requestId ? { request_id: requestId } : {}),
  };
}

async function parseErrorBody(response: Response): Promise<ApiErrorBody | null> {
  try {
    return parseEnvelope(await response.json());
  } catch {
    // HTML proxies, empty bodies and malformed JSON never reach the UI.
    return null;
  }
}

function fallbackCode(status: number): string {
  if (status === 400) return 'invalid_request';
  if (status === 401) return 'unauthorized';
  if (status === 403) return 'forbidden';
  if (status === 404) return 'not_found';
  if (status === 405) return 'method_not_allowed';
  if (status === 413) return 'payload_too_large';
  if (status === 429) return 'rate_limited';
  if (status === 502) return 'upstream';
  if (status === 503) return 'service_unavailable';
  if (status >= 500) return 'internal';
  return 'http_error';
}

function fallbackMessage(status: number): string {
  if (status === 400) return 'The request was not valid.';
  if (status === 401) return 'Authentication is required.';
  if (status === 403) return 'This action is not allowed.';
  if (status === 404) return 'The requested resource was not found.';
  if (status === 405) return 'This operation is not supported.';
  if (status === 413) return 'The submitted data is too large.';
  if (status === 429) return 'Too many requests. Try again later.';
  if (status === 502) return 'The upstream service returned an error.';
  if (status === 503) return 'The service is temporarily unavailable.';
  return status > 0 ? `Request failed (HTTP ${status}).` : 'Request failed.';
}

function elevatedTokenForPath(path: string): string | undefined {
  if (typeof document === 'undefined' || (path !== '/api/account/export' && path !== '/api/account/delete')) {
    return undefined;
  }
  const matches = document.cookie
    .split(';')
    .map((part) => part.trim())
    .filter((part) => part.startsWith('nb_elevated='));
  if (matches.length !== 1) return undefined;
  const token = matches[0].slice('nb_elevated='.length);
  if (token.length === 0 || token.length > 256) return undefined;
  for (const character of token) {
    const codePoint = character.codePointAt(0) ?? 0;
    if (codePoint < 32 || codePoint === 127) return undefined;
  }
  return token;
}

export function isApiError(error: unknown): error is ApiError {
  return error instanceof ApiError;
}

export function isUnauthorized(error: unknown): boolean {
  return isApiError(error) && (error.status === 401 || error.code === 'unauthorized');
}

export function isForbidden(error: unknown): boolean {
  return isApiError(error) && (error.status === 403 || error.code === 'forbidden');
}

export function isNotFoundError(error: unknown): boolean {
  return isApiError(error) && (error.status === 404 || error.code === 'not_found');
}

export async function apiFetch<T>(path: string, options: ApiRequestOptions = {}): Promise<T> {
  const { json, headers, ...init } = options;
  const requestHeaders = new Headers(headers);
  requestHeaders.set('Accept', 'application/json');
  requestHeaders.set('Cache-Control', 'no-store');
  if (json !== undefined) requestHeaders.set('Content-Type', 'application/json');
  const elevatedToken = elevatedTokenForPath(path);
  if (elevatedToken && !requestHeaders.has('X-Elevated-Token')) {
    requestHeaders.set('X-Elevated-Token', elevatedToken);
  }

  let response: Response;
  try {
    response = await fetch(path, {
      ...init,
      body: json !== undefined ? JSON.stringify(json) : init.body,
      cache: 'no-store',
      credentials: 'same-origin',
      headers: requestHeaders,
    });
  } catch {
    throw new ApiError('network_error', 'The network request failed.', 0);
  }

  if (!response.ok) {
    const detail = await parseErrorBody(response);
    throw new ApiError(
      detail?.code ?? fallbackCode(response.status),
      detail?.message ?? fallbackMessage(response.status),
      response.status,
      {
        diag: detail?.diag,
        requestId: detail?.request_id,
      },
    );
  }

  // 202/204 responses and logout/delete operations intentionally have no body.
  if (response.status === 204 || response.status === 202) return undefined as T;
  const contentType = response.headers.get('content-type') ?? '';
  try {
    // The contract uses JSON. A few test/proxy layers omit the content type,
    // so attempt JSON once even when the header is absent; malformed HTML is
    // discarded rather than surfaced to the page.
    return (await response.json()) as T;
  } catch {
    if (!contentType.toLowerCase().includes('json')) return undefined as T;
    throw new ApiError('invalid_response', 'The server returned an invalid response.', response.status);
  }
}
