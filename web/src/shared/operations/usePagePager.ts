import { useCallback, useState } from 'react';
import { isPageNumber, isPageSize, PAGE_SIZES, type PageSize } from './pageNumbers';

export type PageStation = 'admin' | 'user';

export interface UsePagePagerOptions {
  station: PageStation;
  /** A caller-owned stable list name; do not include an account or resource ID. */
  listType: string;
  /** The account or parent resource identity that owns this in-memory page. */
  scopeKey: string;
  /** A committed filter/sort identity. Draft input should not be used here. */
  resetKey?: string;
}

export interface PagePager {
  page: string;
  pageSize: PageSize;
  setPage: (page: string) => void;
  setPageSize: (pageSize: PageSize) => void;
  reset: () => void;
}

export const DEFAULT_PAGE = '1';
export const DEFAULT_PAGE_SIZE: PageSize = 20;

const PAGE_SIZE_STORAGE_VERSION = 'v1';

interface PagerIdentity {
  station: PageStation;
  listType: string;
  scopeKey: string;
  resetKey: string | undefined;
}

interface PagerState {
  identity: PagerIdentity;
  page: string;
  pageSize: PageSize;
}

interface StoredPageSize {
  available: boolean;
  pageSize?: PageSize;
}

// This is deliberately a small preference cache, rather than a registry of
// mounted pagers. Stable list types keep it bounded in practice, and the cap
// also protects callers that accidentally provide dynamic list names.
const MAX_SESSION_PAGE_PREFERENCES = 128;
const sessionPageSizes = new Map<string, PageSize>();
const sessionOnlyStorageKeys = new Set<string>();

function rememberSessionPageSize(storageKey: string, pageSize: PageSize): void {
  sessionPageSizes.delete(storageKey);
  sessionPageSizes.set(storageKey, pageSize);
  while (sessionPageSizes.size > MAX_SESSION_PAGE_PREFERENCES) {
    const oldestKey = sessionPageSizes.keys().next().value;
    if (oldestKey === undefined) break;
    sessionPageSizes.delete(oldestKey);
    sessionOnlyStorageKeys.delete(oldestKey);
  }
}

function forgetSessionPageSize(storageKey: string): void {
  sessionPageSizes.delete(storageKey);
  sessionOnlyStorageKeys.delete(storageKey);
}

function pageSizeStorageKey(station: PageStation, listType: string): string {
  // listType is intentionally the only caller-provided part of this key.
  // Callers must pass a stable module constant, never an account or resource ID.
  return `nonbiri:${station}:${listType}-page-size:${PAGE_SIZE_STORAGE_VERSION}`;
}

function readStoredPageSize(storageKey: string): StoredPageSize {
  if (typeof window === 'undefined') return { available: false };
  try {
    const stored = window.localStorage.getItem(storageKey);
    if (stored !== null && PAGE_SIZES.some((size) => stored === String(size))) {
      return { available: true, pageSize: Number(stored) as PageSize };
    }
    return { available: true };
  } catch {
    return { available: false };
  }
}

function readPageSize(storageKey: string): PageSize {
  const stored = readStoredPageSize(storageKey);
  const remembered = sessionPageSizes.get(storageKey);

  if (!stored.available) {
    // A storage read failure must not discard the preference selected by an
    // earlier mount of the same station/list pair.
    return remembered ?? DEFAULT_PAGE_SIZE;
  }

  if (sessionOnlyStorageKeys.has(storageKey)) {
    // A successful read does not prove that writes work. Keep the preference
    // for this session until a later write succeeds.
    if (remembered !== undefined) return remembered;
    sessionOnlyStorageKeys.delete(storageKey);
  }

  if (stored.pageSize === undefined) {
    // Missing or malformed storage is authoritative when storage is usable;
    // do not resurrect a stale in-memory value after a clear/reset.
    forgetSessionPageSize(storageKey);
    return DEFAULT_PAGE_SIZE;
  }

  rememberSessionPageSize(storageKey, stored.pageSize);
  return stored.pageSize;
}

function writePageSize(storageKey: string, pageSize: PageSize): boolean {
  if (typeof window === 'undefined') return false;
  try {
    window.localStorage.setItem(storageKey, String(pageSize));
    return true;
  } catch {
    return false;
  }
}

/** Read the shared station/list preference for URL-backed pagers. */
export function readPageSizePreference(station: PageStation, listType: string): PageSize {
  return readPageSize(pageSizeStorageKey(station, listType));
}

/** Save the shared station/list preference, retaining the session fallback. */
export function writePageSizePreference(
  station: PageStation,
  listType: string,
  pageSize: PageSize,
): void {
  const storageKey = pageSizeStorageKey(station, listType);
  rememberSessionPageSize(storageKey, pageSize);
  if (writePageSize(storageKey, pageSize)) {
    sessionOnlyStorageKeys.delete(storageKey);
  } else {
    sessionOnlyStorageKeys.add(storageKey);
  }
}

function identityChanged(previous: PagerIdentity, current: PagerIdentity): boolean {
  return (
    previous.station !== current.station ||
    previous.listType !== current.listType ||
    previous.scopeKey !== current.scopeKey ||
    previous.resetKey !== current.resetKey
  );
}

export function usePagePager({
  station,
  listType,
  scopeKey,
  resetKey,
}: UsePagePagerOptions): PagePager {
  const currentIdentity = { station, listType, scopeKey, resetKey } satisfies PagerIdentity;
  const [state, setState] = useState<PagerState>(() => ({
    identity: currentIdentity,
    page: DEFAULT_PAGE,
    pageSize: readPageSizePreference(station, listType),
  }));
  const changed = identityChanged(state.identity, currentIdentity);
  const listChanged = state.identity.station !== station || state.identity.listType !== listType;
  const visiblePageSize = listChanged ? readPageSizePreference(station, listType) : state.pageSize;

  if (changed) {
    setState({
      identity: currentIdentity,
      page: DEFAULT_PAGE,
      pageSize: visiblePageSize,
    });
  }

  const setPage = useCallback((nextPage: string) => {
    if (typeof nextPage !== 'string' || !isPageNumber(nextPage)) return;
    setState((current) => (current.page === nextPage ? current : { ...current, page: nextPage }));
  }, []);

  const setPageSize = useCallback(
    (nextPageSize: PageSize) => {
      if (!isPageSize(nextPageSize)) return;
      setState((current) =>
        current.page === DEFAULT_PAGE && current.pageSize === nextPageSize
          ? current
          : { ...current, page: DEFAULT_PAGE, pageSize: nextPageSize },
      );
      writePageSizePreference(station, listType, nextPageSize);
    },
    [station, listType],
  );

  const reset = useCallback(() => {
    setState((current) =>
      current.page === DEFAULT_PAGE ? current : { ...current, page: DEFAULT_PAGE },
    );
  }, []);

  return {
    page: changed ? DEFAULT_PAGE : state.page,
    pageSize: visiblePageSize,
    setPage,
    setPageSize,
    reset,
  };
}
