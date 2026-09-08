import { useCallback, useEffect, useRef, useState } from 'react';
import { useLocation, useNavigationType, useSearchParams } from 'react-router';
import { isPageNumber, isPageSize, PAGE_SIZES, type PageSize } from './pageNumbers';
import {
  DEFAULT_PAGE,
  readPageSizePreference,
  type PagePager,
  type PageStation,
  writePageSizePreference,
} from './usePagePager';

export interface UseUrlPagePagerOptions {
  station: PageStation;
  /** A caller-owned stable list name; do not include an account or resource ID. */
  listType: string;
  /** The account or parent resource identity that owns this page context. */
  scopeKey: string;
  /** Whether the account or parent scope has been confirmed by the session authority. */
  scopeReady?: boolean;
  /** A committed filter/sort identity. Draft input should not be used here. */
  resetKey?: string;
  /** Stable query parameter names for the page and page size. */
  pageParam?: string;
  pageSizeParam?: string;
}

interface UrlPagerIdentity {
  station: PageStation;
  listType: string;
  scopeKey: string;
  scopeReady: boolean;
  resetKey: string | undefined;
  pageParam: string;
  pageSizeParam: string;
  locationKey: string;
}

interface UrlPagerState {
  identity: UrlPagerIdentity;
  pendingReset: boolean;
  resetPageSize?: PageSize;
}

interface ParsedUrlPager {
  page: string;
  pageValue: string | undefined;
  pageValueCount: number;
  pageNeedsNormalization: boolean;
  pageSize: PageSize;
  pageSizeNeedsNormalization: boolean;
}

const DEFAULT_PAGE_PARAM = 'page';
const DEFAULT_PAGE_SIZE_PARAM = 'page_size';

function resolvedParamName(value: string | undefined, fallback: string): string {
  return value && value.length > 0 ? value : fallback;
}

function parsePageSize(value: string | undefined): PageSize | undefined {
  if (value === undefined) return undefined;
  const numeric = Number(value);
  return isPageSize(numeric) && PAGE_SIZES.some((size) => value === String(size))
    ? numeric
    : undefined;
}

function parseUrlPager(
  searchParams: URLSearchParams,
  pageParam: string,
  pageSizeParam: string,
  storedPageSize: PageSize,
): ParsedUrlPager {
  const pageValues = searchParams.getAll(pageParam);
  const pageValue = pageValues.length === 1 ? pageValues[0] : undefined;
  const pageValid = pageValues.length === 1 && isPageNumber(pageValue);
  const page = pageValid ? pageValue : DEFAULT_PAGE;

  const pageSizeValues = searchParams.getAll(pageSizeParam);
  const pageSizeValue = pageSizeValues.length === 1 ? pageSizeValues[0] : undefined;
  const parsedPageSize = parsePageSize(pageSizeValue);

  return {
    page,
    pageValue,
    pageValueCount: pageValues.length,
    pageNeedsNormalization: pageValues.length > 0 && !pageValid,
    pageSize: parsedPageSize ?? storedPageSize,
    pageSizeNeedsNormalization: pageSizeValues.length > 0 && parsedPageSize === undefined,
  };
}

function samePagerIdentity(previous: UrlPagerIdentity, current: UrlPagerIdentity): boolean {
  return (
    previous.station === current.station &&
    previous.listType === current.listType &&
    previous.scopeKey === current.scopeKey &&
    previous.scopeReady === current.scopeReady &&
    previous.resetKey === current.resetKey &&
    previous.pageParam === current.pageParam &&
    previous.pageSizeParam === current.pageSizeParam
  );
}

function sameListContext(previous: UrlPagerIdentity, current: UrlPagerIdentity): boolean {
  return (
    previous.station === current.station &&
    previous.listType === current.listType &&
    previous.pageParam === current.pageParam &&
    previous.pageSizeParam === current.pageSizeParam
  );
}

export function useUrlPagePager({
  station,
  listType,
  scopeKey,
  scopeReady = true,
  resetKey,
  pageParam,
  pageSizeParam,
}: UseUrlPagePagerOptions): PagePager {
  const resolvedPageParam = resolvedParamName(pageParam, DEFAULT_PAGE_PARAM);
  const resolvedPageSizeParam = resolvedParamName(pageSizeParam, DEFAULT_PAGE_SIZE_PARAM);
  const location = useLocation();
  const navigationType = useNavigationType();
  const [searchParams, setSearchParams] = useSearchParams();
  const currentIdentity = {
    station,
    listType,
    scopeKey,
    scopeReady,
    resetKey,
    pageParam: resolvedPageParam,
    pageSizeParam: resolvedPageSizeParam,
    locationKey: location.key,
  } satisfies UrlPagerIdentity;
  const [state, setState] = useState<UrlPagerState>(() => ({
    identity: currentIdentity,
    pendingReset: false,
  }));
  const lastNormalizationSignature = useRef<string | undefined>(undefined);

  const identityMatches = samePagerIdentity(state.identity, currentIdentity);
  const locationChanged = state.identity.locationKey !== location.key;
  const listContextMatches = sameListContext(state.identity, currentIdentity);
  const confirmedScopeChanged =
    state.identity.scopeReady && scopeReady && state.identity.scopeKey !== scopeKey;
  const scopeReadinessChanged = state.identity.scopeReady !== scopeReady;
  const initialScopeConfirmation =
    !state.identity.scopeReady &&
    scopeReady &&
    listContextMatches &&
    state.identity.resetKey === resetKey;
  const unconfirmedScopeUpdate =
    !state.identity.scopeReady &&
    !scopeReady &&
    listContextMatches &&
    state.identity.resetKey === resetKey;
  const popRestore =
    locationChanged &&
    navigationType === 'POP' &&
    listContextMatches &&
    !scopeReadinessChanged &&
    !confirmedScopeChanged;
  const resetRequired =
    !identityMatches && !initialScopeConfirmation && !unconfirmedScopeUpdate && !popRestore;
  const storedPageSize = readPageSizePreference(station, listType);
  const parsed = parseUrlPager(
    searchParams,
    resolvedPageParam,
    resolvedPageSizeParam,
    storedPageSize,
  );
  const resetPageSize = listContextMatches ? state.resetPageSize : storedPageSize;
  if (resetPageSize !== undefined) {
    parsed.pageSize = resetPageSize;
    parsed.pageSizeNeedsNormalization =
      searchParams.getAll(resolvedPageSizeParam).length !== 1 ||
      searchParams.get(resolvedPageSizeParam) !== String(resetPageSize);
  }
  const pendingReset = state.pendingReset || resetRequired;
  const resetNeedsPageUpdate =
    pendingReset &&
    !(
      parsed.pageValueCount === 0 ||
      (parsed.pageValueCount === 1 && parsed.pageValue === DEFAULT_PAGE)
    );

  if (resetRequired) {
    if (!state.pendingReset || !identityMatches || state.identity.locationKey !== location.key) {
      setState({ identity: currentIdentity, pendingReset: true, resetPageSize });
    }
  } else if (state.pendingReset && !resetNeedsPageUpdate && !parsed.pageSizeNeedsNormalization) {
    setState({ identity: currentIdentity, pendingReset: false });
  } else if (!identityMatches || locationChanged) {
    setState({ identity: currentIdentity, pendingReset: false });
  }

  useEffect(() => {
    const shouldNormalizePage = pendingReset ? resetNeedsPageUpdate : parsed.pageNeedsNormalization;
    if (!shouldNormalizePage && !parsed.pageSizeNeedsNormalization) return;
    const signature = [
      location.key,
      location.search,
      shouldNormalizePage,
      parsed.pageSizeNeedsNormalization,
      parsed.pageSize,
    ].join('|');
    if (lastNormalizationSignature.current === signature) return;
    lastNormalizationSignature.current = signature;

    setSearchParams(
      (previous) => {
        const next = new URLSearchParams(previous);
        if (shouldNormalizePage) {
          next.delete(resolvedPageParam);
          next.set(resolvedPageParam, DEFAULT_PAGE);
        }
        if (parsed.pageSizeNeedsNormalization) {
          next.delete(resolvedPageSizeParam);
          next.set(resolvedPageSizeParam, String(parsed.pageSize));
        }
        return next;
      },
      { replace: true },
    );
  }, [
    parsed.page,
    parsed.pageNeedsNormalization,
    parsed.pageSize,
    parsed.pageSizeNeedsNormalization,
    pendingReset,
    resetNeedsPageUpdate,
    location.key,
    location.search,
    resolvedPageParam,
    resolvedPageSizeParam,
    setSearchParams,
  ]);

  const setPage = useCallback(
    (nextPage: string) => {
      if (typeof nextPage !== 'string' || !isPageNumber(nextPage)) return;
      setSearchParams((previous) => {
        const next = new URLSearchParams(previous);
        next.delete(resolvedPageParam);
        next.set(resolvedPageParam, nextPage);
        next.delete(resolvedPageSizeParam);
        next.set(resolvedPageSizeParam, String(parsed.pageSize));
        return next;
      });
    },
    [parsed.pageSize, resolvedPageParam, resolvedPageSizeParam, setSearchParams],
  );

  const setPageSize = useCallback(
    (nextPageSize: PageSize) => {
      if (!isPageSize(nextPageSize)) return;
      writePageSizePreference(station, listType, nextPageSize);
      setSearchParams((previous) => {
        const next = new URLSearchParams(previous);
        next.delete(resolvedPageParam);
        next.set(resolvedPageParam, DEFAULT_PAGE);
        next.delete(resolvedPageSizeParam);
        next.set(resolvedPageSizeParam, String(nextPageSize));
        return next;
      });
    },
    [listType, resolvedPageParam, resolvedPageSizeParam, setSearchParams, station],
  );

  const reset = useCallback(() => {
    if (parsed.page === DEFAULT_PAGE && !parsed.pageNeedsNormalization) return;
    setSearchParams(
      (previous) => {
        const next = new URLSearchParams(previous);
        next.delete(resolvedPageParam);
        next.set(resolvedPageParam, DEFAULT_PAGE);
        return next;
      },
      { replace: true },
    );
  }, [parsed.page, parsed.pageNeedsNormalization, resolvedPageParam, setSearchParams]);

  return {
    page: pendingReset ? DEFAULT_PAGE : parsed.page,
    pageSize: parsed.pageSize,
    setPage,
    setPageSize,
    reset,
  };
}
