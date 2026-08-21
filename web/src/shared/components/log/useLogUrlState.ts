import { useCallback, useMemo } from 'react';
import { useSearchParams } from 'react-router';

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
  /** Inclusive upper time bound in unix seconds; undefined when unset. */
  toUnix?: number;
}

/** Maximum characters accepted for any restored filter value. */
const MAX_FILTER_CHARS = 512;

function parsePositiveInt(raw: string | null): number | undefined {
  if (raw === null || !raw.trim()) return undefined;
  const value = Number(raw);
  return Number.isSafeInteger(value) && value >= 0 ? value : undefined;
}

function parseState(
  params: URLSearchParams,
  textParams: readonly string[],
  defaultPageSize: number,
): LogUrlState {
  const filters: Record<string, string> = {};
  for (const name of textParams) {
    const raw = params.get(name);
    // Repeated or over-long values are ignored rather than partially applied.
    if (raw === null || raw.length > MAX_FILTER_CHARS) continue;
    const trimmed = raw.trim();
    if (trimmed) filters[name] = trimmed;
  }
  const page = parsePositiveInt(params.get('page')) ?? 1;
  const pageSize = parsePositiveInt(params.get('page_size')) ?? defaultPageSize;
  return {
    page: Math.max(1, page),
    pageSize: Math.min(100, Math.max(1, pageSize)),
    filters,
    fromUnix: parsePositiveInt(params.get('from')),
    toUnix: parsePositiveInt(params.get('to')),
  };
}

/**
 * Two-way binding between log list state and the URL query string. `patch`
 * merges partial changes into the current state and rewrites the query string
 * (default values are omitted so URLs stay clean). Uses `replace` so paging
 * and filtering do not spam browser history.
 */
export function useLogUrlState(
  textParams: readonly string[],
  defaultPageSize: number,
): { state: LogUrlState; patch: (partial: Partial<LogUrlState>) => void } {
  const [searchParams, setSearchParams] = useSearchParams();

  const state = useMemo(
    () => parseState(searchParams, textParams, defaultPageSize),
    [searchParams, textParams, defaultPageSize],
  );

  const patch = useCallback(
    (partial: Partial<LogUrlState>) => {
      setSearchParams(
        (prev) => {
          const next = { ...parseState(prev, textParams, defaultPageSize), ...partial };
          const params = new URLSearchParams();
          if (next.page > 1) params.set('page', String(next.page));
          if (next.pageSize !== defaultPageSize) params.set('page_size', String(next.pageSize));
          for (const name of textParams) {
            const value = next.filters[name];
            if (value) params.set(name, value);
          }
          if (next.fromUnix !== undefined && next.fromUnix > 0) {
            params.set('from', String(next.fromUnix));
          }
          if (next.toUnix !== undefined && next.toUnix > 0) {
            params.set('to', String(next.toUnix));
          }
          return params;
        },
        { replace: true },
      );
    },
    [setSearchParams, textParams, defaultPageSize],
  );

  return { state, patch };
}
