import { QueryClient } from '@tanstack/react-query';

/** Shared defaults applied to the query client of every station. */
export const QUERY_DEFAULTS = {
  staleTime: 30_000,
  gcTime: 5 * 60_000,
  retry: 1,
  retryDelay: (attempt: number) => Math.min(1_000 * 2 ** attempt, 10_000),
  refetchOnWindowFocus: false,
} as const;

export function createQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: {
        staleTime: QUERY_DEFAULTS.staleTime,
        gcTime: QUERY_DEFAULTS.gcTime,
        retry: QUERY_DEFAULTS.retry,
        retryDelay: QUERY_DEFAULTS.retryDelay,
        refetchOnWindowFocus: QUERY_DEFAULTS.refetchOnWindowFocus,
      },
      mutations: {
        retry: 0,
      },
    },
  });
}
