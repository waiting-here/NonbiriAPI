import { QueryClient } from '@tanstack/react-query';
import { isApiError } from './http';

/** Shared defaults applied to the query client of every station. */
export const QUERY_DEFAULTS = {
  staleTime: 30_000,
  gcTime: 5 * 60_000,
  retry: 1,
  retryDelay: (attempt: number) => Math.min(1_000 * 2 ** attempt, 10_000),
  refetchOnWindowFocus: false,
} as const;

function shouldRetry(failureCount: number, error: unknown): boolean {
  // Auth, validation, not-found and rate-limit responses are deterministic for
  // the current request. Retrying them creates noise and can repeat mutations
  // when a caller accidentally uses a query for a write operation.
  if (
    isApiError(error) &&
    (error.status === 400 ||
      error.status === 401 ||
      error.status === 403 ||
      error.status === 404 ||
      error.status === 405 ||
      error.status === 413 ||
      error.status === 429)
  ) {
    return false;
  }
  return failureCount < QUERY_DEFAULTS.retry;
}

export function createQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: {
        staleTime: QUERY_DEFAULTS.staleTime,
        gcTime: QUERY_DEFAULTS.gcTime,
        retry: shouldRetry,
        retryDelay: QUERY_DEFAULTS.retryDelay,
        refetchOnWindowFocus: QUERY_DEFAULTS.refetchOnWindowFocus,
      },
      mutations: {
        retry: 0,
        // One-time secrets must never be returned by a useQuery. Pages that
        // generate one use a direct request and hold the result in local state.
        gcTime: 0,
      },
    },
  });
}
