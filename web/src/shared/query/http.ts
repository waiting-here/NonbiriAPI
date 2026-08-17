/**
 * Single request path for both stations (skeleton).
 *
 * No real endpoints exist yet; this establishes the stable error shape and the
 * interception points (credential attachment, 401 handling, error
 * normalization) that page code builds upon once the API contract is frozen.
 * The backend error envelope is expected to carry at least { code, message };
 * align the exact shape here when the API contract lands.
 */

export interface ApiErrorBody {
  code: string;
  message: string;
}

export class ApiError extends Error {
  readonly code: string;
  readonly status: number;

  constructor(code: string, message: string, status: number) {
    super(message);
    this.name = 'ApiError';
    this.code = code;
    this.status = status;
  }
}

export interface ApiRequestOptions extends RequestInit {
  /** Object payload, serialized to JSON automatically. */
  json?: unknown;
}

async function parseErrorBody(response: Response): Promise<ApiErrorBody | null> {
  try {
    const body: unknown = await response.json();
    if (
      body !== null &&
      typeof body === 'object' &&
      'code' in body &&
      typeof body.code === 'string' &&
      'message' in body &&
      typeof body.message === 'string'
    ) {
      return body as ApiErrorBody;
    }
  } catch {
    // Non-JSON error body; normalized below.
  }
  return null;
}

export async function apiFetch<T>(path: string, options: ApiRequestOptions = {}): Promise<T> {
  const { json, headers, ...init } = options;

  const requestHeaders = new Headers(headers);
  requestHeaders.set('Accept', 'application/json');
  if (json !== undefined) {
    requestHeaders.set('Content-Type', 'application/json');
  }
  // Interception point: attach session credentials / CSRF headers here once the
  // session contract is frozen.
  // Credentials and secrets must never be cached; 'no-store' stays for key /
  // config endpoints regardless of later tuning.
  requestHeaders.set('Cache-Control', 'no-store');

  let response: Response;
  try {
    response = await fetch(path, {
      ...init,
      headers: requestHeaders,
      body: json !== undefined ? JSON.stringify(json) : init.body,
    });
  } catch {
    throw new ApiError('network_error', 'Network request failed', 0);
  }

  if (!response.ok) {
    const detail = await parseErrorBody(response);
    throw new ApiError(
      detail?.code ?? 'http_error',
      detail?.message ?? `Request failed with status ${response.status}`,
      response.status,
    );
  }

  // Interception point: 401 handling (session refresh / redirect) once auth exists.
  return (await response.json()) as T;
}
