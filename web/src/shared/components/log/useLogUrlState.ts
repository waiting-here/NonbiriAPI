import { useCallback, useMemo } from 'react';
import { useSearchState } from '@shared/operations/useSearchState';

// URL-backed state for the shared log screens. Page, page size, text filters,
// and the unix-second time range live in the query string so a filtered view
// survives a refresh and can be shared as a link. Values restored from the
// URL are parsed defensively: anything malformed falls back to the default
// instead of reaching the API.

export interface LogUrlState {
  page: number;
  pageSize: number;
  /** Raw single-valued text filters keyed by query parameter name. */
  filters: Record<string, string>;
  /** Inclusive lower time bound in unix seconds; undefined when unset. */
  fromUnix?: number;
  /** Exclusive upper time bound in unix seconds; undefined when unset. */
  toUnix?: number;
}

/** Maximum characters accepted for any restored filter value. */
const MAX_FILTER_CHARS = 512;

function parsePositiveInt(raw: string | null): number | undefined {
  if (raw === null || !/^(0|[1-9][0-9]{0,11})$/.test(raw)) return undefined;
  const value = Number(raw);
  return Number.isSafeInteger(value) && value >= 0 && value <= 253_402_300_799 ? value : undefined;
}

function parseState(
  params: URLSearchParams,
  textParams: readonly string[],
  defaultPageSize: number,
): LogUrlState {
  const filters: Record<string, string> = {};
  for (const name of textParams) {
    const values = params.getAll(name);
    const raw = values.length === 1 ? values[0] : null;
    // Repeated or over-long values are ignored rather than partially applied.
    if (raw === null || raw.length > MAX_FILTER_CHARS) continue;
    const trimmed = raw.trim();
    if (Array.from(trimmed).some((c) => c.charCodeAt(0) < 32 || c.charCodeAt(0) === 127)) continue;
    if (
      (name === 'user_id' || name === 'endpoint_key_id') &&
      (!/^[1-9][0-9]{0,18}$/.test(trimmed) || BigInt(trimmed) > 9_223_372_036_854_775_807n)
    )
      continue;
    if (name === 'status' && !/^[1-5][0-9]{2}$/.test(trimmed)) continue;
    if (name === 'phase' && trimmed !== 'handler' && trimmed !== 'pre_handler') continue;
    if (trimmed) filters[name] = trimmed;
  }
  const page = parsePositiveInt(params.get('page')) ?? 1;
  const pageSize = parsePositiveInt(params.get('page_size')) ?? defaultPageSize;
  return {
    page: Math.max(1, page),
    pageSize: Math.min(100, Math.max(1, pageSize)),
    filters,
    fromUnix: params.getAll('from').length === 1 ? parsePositiveInt(params.get('from')) : undefined,
    toUnix: params.getAll('to').length === 1 ? parsePositiveInt(params.get('to')) : undefined,
  };
}

/**
 * Two-way binding between log list state and the URL query string. `patch`
 * merges partial changes into the current state and rewrites the query string
 * while preserving unrelated query parameters and browser history.
 */
export function useLogUrlState(
  textParams: readonly string[],
  defaultPageSize: number,
): {
  state: LogUrlState;
  patch: (
    partial: Partial<LogUrlState>,
    options?: { clearDetail?: boolean; replace?: boolean },
  ) => void;
} {
  const [searchParams, setSearchParams] = useSearchState();

  const state = useMemo(
    () => parseState(searchParams, textParams, defaultPageSize),
    [searchParams, textParams, defaultPageSize],
  );

  const patch = useCallback(
    (partial: Partial<LogUrlState>, options?: { clearDetail?: boolean; replace?: boolean }) => {
      setSearchParams(
        (prev) => {
          const next = { ...parseState(prev, textParams, defaultPageSize), ...partial };
          const params = new URLSearchParams(prev);
          for (const name of textParams) {
            params.delete(name);
            const value = next.filters?.[name];
            if (value) params.set(name, value);
          }
          params.delete('from');
          if (next.fromUnix !== undefined && next.fromUnix >= 0) {
            params.set('from', String(next.fromUnix));
          }
          params.delete('to');
          if (next.toUnix !== undefined && next.toUnix >= 0) {
            params.set('to', String(next.toUnix));
          }
          if (partial.page !== undefined) {
            params.delete('page');
            params.set('page', String(next.page));
          }
          if (partial.pageSize !== undefined) {
            params.delete('page_size');
            params.set('page_size', String(next.pageSize));
          }
          if (options?.clearDetail) {
            params.delete('request_id');
            params.delete('attempt_page');
            params.delete('attempt_page_size');
          }
          return params;
        },
        { replace: options?.replace ?? false },
      );
    },
    [setSearchParams, textParams, defaultPageSize],
  );

  return { state, patch };
}
