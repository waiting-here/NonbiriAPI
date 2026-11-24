import { useLocation } from 'react-router';
import { useCallback, useEffect, useMemo } from 'react';
import { useSearchState } from '@shared/operations/useSearchState';
import { isLogOperation, validLogDiscordID, validLogResourceID } from './data';
import { ATTEMPT_SEQ_PARAM } from './managementFailures';

// Public controls can be shared. Private filters remain scoped to this role and
// account in the history entry, including filters in links saved on older versions.
const privateFields = new Set(['endpoint_base_url', 'upstream_model', 'model', 'error_code']);

export const relativeLogSeconds = { '15m': 900, '1h': 3600, '24h': 86400, '7d': 604800 } as const;
export type RelativeLogRange = keyof typeof relativeLogSeconds;
export interface LogUrlState {
  range?: RelativeLogRange;
  page: number;
  pageSize: number;
  /** Raw single-valued text filters keyed by query parameter name. */
  filters: Record<string, string>;
  /** Inclusive lower time bound in unix seconds; undefined when unset. */
  fromUnix?: number;
  /** Exclusive upper time bound in unix seconds; undefined when unset. */
  toUnix?: number;
}

interface LogPatchOptions {
  clearDetail?: boolean;
  replace?: boolean;
  keepFilters?: readonly string[];
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
    if ((name === 'user_id' || name === 'endpoint_key_id') && !validLogResourceID(trimmed))
      continue;
    if (name === 'discord_id' && !validLogDiscordID(trimmed)) continue;
    if (name === 'operation' && !isLogOperation(raw)) continue;
    if (name === 'status' && !/^[1-5][0-9]{2}$/.test(trimmed)) continue;
    if (
      name === 'outcome' &&
      !['success', 'failed', 'before_dispatch', 'actual_backoff'].includes(trimmed)
    )
      continue;
    if (name === 'phase' && trimmed !== 'handler' && trimmed !== 'pre_handler') continue;
    if (name === 'usage_total_mismatch' && trimmed !== 'true') continue;
    if (trimmed) filters[name] = trimmed;
  }
  const page = parsePositiveInt(params.get('page')) ?? 1;
  const pageSize = parsePositiveInt(params.get('page_size')) ?? defaultPageSize;
  const from =
    params.getAll('from').length === 1 ? parsePositiveInt(params.get('from')) : undefined;
  const to = params.getAll('to').length === 1 ? parsePositiveInt(params.get('to')) : undefined;
  const rawRange = params.getAll('range');
  const range =
    rawRange.length === 1 &&
    Object.hasOwn(relativeLogSeconds, rawRange[0]) &&
    from === undefined &&
    to === undefined
      ? (rawRange[0] as RelativeLogRange)
      : undefined;
  const reversed = from !== undefined && to !== undefined && from >= to;
  return {
    page: Math.max(1, page),
    pageSize: Math.min(100, Math.max(1, pageSize)),
    filters,
    ...(range && { range }),
    fromUnix: reversed ? undefined : from,
    toUnix: reversed ? undefined : to,
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
  scopeKey = '',
): {
  state: LogUrlState;
  patch: (partial: Partial<LogUrlState>, options?: LogPatchOptions) => void;
} {
  const [searchParams, setSearchParams] = useSearchState();
  const location = useLocation();
  const state = useMemo(() => {
    const source = new URLSearchParams(searchParams);
    const saved = location.state?.logPrivate;
    if (
      scopeKey &&
      saved?.scope === scopeKey &&
      saved.filters &&
      typeof saved.filters === 'object'
    ) {
      for (const [key, value] of Object.entries(saved.filters)) {
        if (privateFields.has(key) && typeof value === 'string') source.set(key, value);
      }
    }
    return parseState(source, textParams, defaultPageSize);
  }, [searchParams, textParams, defaultPageSize, location.state, scopeKey]);

  useEffect(() => {
    if (!scopeKey || !Array.from(privateFields).some((key) => searchParams.has(key))) return;
    const filters = Object.fromEntries(
      Object.entries(state.filters).filter(([key]) => privateFields.has(key)),
    );
    setSearchParams(
      (previous) => {
        const next = new URLSearchParams(previous);
        for (const key of privateFields) next.delete(key);
        return next;
      },
      { replace: true, state: { ...location.state, logPrivate: { scope: scopeKey, filters } } },
    );
  }, [scopeKey, searchParams, state.filters, setSearchParams, location.state]);

  const patch = useCallback(
    (partial: Partial<LogUrlState>, options?: LogPatchOptions) => {
      const localFilters = Object.fromEntries(
        Object.entries(partial.filters ?? state.filters).filter(
          ([key]) => scopeKey && privateFields.has(key),
        ),
      );
      setSearchParams(
        (prev) => {
          const next = { ...parseState(prev, textParams, defaultPageSize), ...partial };
          const params = new URLSearchParams(prev);
          for (const name of textParams) {
            if (options?.keepFilters?.includes(name)) continue;
            params.delete(name);
            const value = next.filters?.[name];
            if (value && (!scopeKey || !privateFields.has(name))) params.set(name, value);
          }
          params.delete('range');
          if (next.range && next.fromUnix === undefined && next.toUnix === undefined)
            params.set('range', next.range);
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
            params.delete(ATTEMPT_SEQ_PARAM);
          }
          return params;
        },
        {
          replace: options?.replace ?? false,
          ...(scopeKey
            ? {
                state: {
                  ...location.state,
                  logPrivate: { scope: scopeKey, filters: localFilters },
                },
              }
            : {}),
        },
      );
    },
    [setSearchParams, textParams, defaultPageSize, state.filters, scopeKey, location.state],
  );

  return { state, patch };
}
