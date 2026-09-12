import { useCallback, useEffect, useMemo, useState } from 'react';
import { useSearchState } from '@shared/operations/useSearchState';
import {
  DEFAULT_PAGE,
  DEFAULT_PAGE_SIZE,
  readPageSizePreference,
  writePageSizePreference,
} from '@shared/operations/usePagePager';
import { PAGE_SIZES, type PageSize } from '@shared/operations/pageNumbers';
import {
  HISTORY_CATEGORIES,
  MAX_HISTORY_UNIX_SECOND,
  isHistoryAnchor,
  isHistoryPage,
  type HistoryFilter,
} from './data';

export const CREDIT_HISTORY_LIST_TYPE = 'credit-history';

const HISTORY_URL_PARAMS = [
  'asset_type',
  'page',
  'page_size',
  'anchor',
  'from',
  'to',
  'category',
  'direction',
] as const;
const HISTORY_URL_PARAM_SET = new Set<string>(HISTORY_URL_PARAMS);

type HistoryCategory = (typeof HISTORY_CATEGORIES)[number];
type HistoryDirection = 'income' | 'expense';

interface SingleValue {
  present: boolean;
  value?: string;
  invalid: boolean;
}

export interface CreditHistoryUrlState {
  filter: HistoryFilter;
  canonicalSearch: string;
  invalidPage: boolean;
  invalidPageSize: boolean;
  invalidAnchor: boolean;
  invalidFrom: boolean;
  invalidTo: boolean;
  invalidAsset: boolean;
  invalidCategory: boolean;
  invalidDirection: boolean;
  invalidRange: boolean;
  unknownParameter: boolean;
}

export interface CreditHistoryFilterDraft {
  asset_type?: HistoryFilter['asset_type'];
  category?: string;
  direction?: string;
  from?: number;
  to?: number;
}

export interface CreditHistoryUrlController extends CreditHistoryUrlState {
  refreshRevision: number;
  setPage: (page: string, anchor?: string | null) => void;
  setPageSize: (pageSize: PageSize, anchor?: string | null) => void;
  apply: (draft: CreditHistoryFilterDraft) => boolean;
  reset: () => void;
  refresh: () => void;
}

function singleValue(params: URLSearchParams, name: string): SingleValue {
  const values = params.getAll(name);
  if (values.length === 0) return { present: false, invalid: false };
  if (values.length !== 1 || values[0] === '') return { present: true, invalid: true };
  return { present: true, value: values[0], invalid: false };
}

function parseUnixSecond(value: SingleValue): number | undefined {
  if (value.value === undefined || !/^(0|[1-9][0-9]*)$/.test(value.value)) return undefined;
  try {
    const parsed = BigInt(value.value);
    if (parsed > BigInt(MAX_HISTORY_UNIX_SECOND)) return undefined;
    return Number(parsed);
  } catch {
    return undefined;
  }
}

function serializeHistoryValues(values: {
  asset_type?: HistoryFilter['asset_type'];
  page: string;
  pageSize: PageSize;
  includePage: boolean;
  includePageSize: boolean;
  anchor?: string;
  from?: number;
  to?: number;
  category?: string;
  direction?: string;
}): string {
  const params = new URLSearchParams();
  if (values.asset_type !== undefined) params.set('asset_type', values.asset_type);
  if (values.includePage || values.page !== DEFAULT_PAGE) params.set('page', values.page);
  if (values.includePageSize) params.set('page_size', String(values.pageSize));
  if (values.anchor !== undefined) params.set('anchor', values.anchor);
  if (values.from !== undefined) params.set('from', String(values.from));
  if (values.to !== undefined) params.set('to', String(values.to));
  if (values.category !== undefined) params.set('category', values.category);
  if (values.direction !== undefined) params.set('direction', values.direction);
  return params.toString();
}

function validAsset(value: string | undefined): value is NonNullable<HistoryFilter['asset_type']> {
  return value === 'general' || value === 'game' || value === 'all';
}

function validCategory(value: string | undefined): value is HistoryCategory {
  return value !== undefined && HISTORY_CATEGORIES.some((category) => category === value);
}

function validDirection(value: string | undefined): value is HistoryDirection {
  return value === 'income' || value === 'expense';
}

function valuesFromState(state: CreditHistoryUrlState) {
  return {
    asset_type: state.filter.asset_type,
    page: state.filter.page,
    pageSize: state.filter.page_size,
    includePage: state.filter.page !== DEFAULT_PAGE,
    includePageSize: true,
    anchor: state.filter.anchor,
    from: state.filter.from,
    to: state.filter.to,
    category: state.filter.category,
    direction: state.filter.direction,
  };
}

export function parseCreditHistorySearch(
  input: URLSearchParams | string,
  storedPageSize = DEFAULT_PAGE_SIZE,
): CreditHistoryUrlState {
  const params =
    typeof input === 'string' ? new URLSearchParams(input) : new URLSearchParams(input.toString());
  const fallbackPageSize = PAGE_SIZES.includes(storedPageSize as PageSize)
    ? (storedPageSize as PageSize)
    : DEFAULT_PAGE_SIZE;
  const pageParam = singleValue(params, 'page');
  const pageValid = !pageParam.invalid && isHistoryPage(pageParam.value);
  const page = pageValid ? pageParam.value! : DEFAULT_PAGE;

  const pageSizeParam = singleValue(params, 'page_size');
  const parsedPageSize = pageSizeParam.value
    ? PAGE_SIZES.find((size) => String(size) === pageSizeParam.value)
    : undefined;
  const pageSize = !pageSizeParam.invalid && parsedPageSize ? parsedPageSize : fallbackPageSize;

  const anchorParam = singleValue(params, 'anchor');
  const anchor =
    !anchorParam.invalid && isHistoryAnchor(anchorParam.value) ? anchorParam.value : undefined;
  const fromParam = singleValue(params, 'from');
  const toParam = singleValue(params, 'to');
  let from = !fromParam.invalid ? parseUnixSecond(fromParam) : undefined;
  let to = !toParam.invalid ? parseUnixSecond(toParam) : undefined;
  const invalidRange = from !== undefined && to !== undefined && from >= to;
  if (invalidRange) {
    from = undefined;
    to = undefined;
  }

  const assetParam = singleValue(params, 'asset_type');
  const asset_type =
    !assetParam.invalid && validAsset(assetParam.value) ? assetParam.value : undefined;
  const categoryParam = singleValue(params, 'category');
  const category =
    !categoryParam.invalid && validCategory(categoryParam.value) ? categoryParam.value : undefined;
  const directionParam = singleValue(params, 'direction');
  const direction =
    !directionParam.invalid && validDirection(directionParam.value)
      ? directionParam.value
      : undefined;
  const filter: HistoryFilter = {
    ...(asset_type !== undefined ? { asset_type } : {}),
    page,
    page_size: pageSize,
    ...(anchor !== undefined ? { anchor } : {}),
    ...(from !== undefined ? { from } : {}),
    ...(to !== undefined ? { to } : {}),
    ...(category !== undefined ? { category } : {}),
    ...(direction !== undefined ? { direction } : {}),
  };
  const canonicalSearch = serializeHistoryValues({
    asset_type,
    page,
    pageSize,
    includePage: pageParam.present,
    includePageSize: pageSizeParam.present,
    anchor,
    from,
    to,
    category,
    direction,
  });
  return {
    filter,
    canonicalSearch,
    invalidPage: pageParam.present && !pageValid,
    invalidPageSize: pageSizeParam.present && (pageSizeParam.invalid || !parsedPageSize),
    invalidAnchor: anchorParam.present && (anchorParam.invalid || anchor === undefined),
    invalidFrom:
      fromParam.present && (fromParam.invalid || parseUnixSecond(fromParam) === undefined),
    invalidTo: toParam.present && (toParam.invalid || parseUnixSecond(toParam) === undefined),
    invalidAsset: assetParam.present && (assetParam.invalid || asset_type === undefined),
    invalidCategory: categoryParam.present && (categoryParam.invalid || category === undefined),
    invalidDirection: directionParam.present && (directionParam.invalid || direction === undefined),
    invalidRange,
    unknownParameter: [...params.keys()].some((key) => !HISTORY_URL_PARAM_SET.has(key)),
  };
}

function validDraft(draft: CreditHistoryFilterDraft): boolean {
  if (draft.asset_type !== undefined && !validAsset(draft.asset_type)) return false;
  if (draft.category !== undefined && !validCategory(draft.category)) return false;
  if (draft.direction !== undefined && !validDirection(draft.direction)) return false;
  if (
    draft.from !== undefined &&
    (!Number.isSafeInteger(draft.from) || draft.from < 0 || draft.from > MAX_HISTORY_UNIX_SECOND)
  )
    return false;
  if (
    draft.to !== undefined &&
    (!Number.isSafeInteger(draft.to) || draft.to < 0 || draft.to > MAX_HISTORY_UNIX_SECOND)
  )
    return false;
  return !(draft.from !== undefined && draft.to !== undefined && draft.from >= draft.to);
}

export function useCreditHistoryUrl(scopeReset = false): CreditHistoryUrlController {
  const [searchParams, setSearchParams] = useSearchState();
  const [refreshRevision, setRefreshRevision] = useState(0);
  const [pendingScopeReset, setPendingScopeReset] = useState(scopeReset);
  if (scopeReset && !pendingScopeReset) setPendingScopeReset(true);
  const rawSearch = searchParams.toString();
  const storedPageSize = readPageSizePreference('user', CREDIT_HISTORY_LIST_TYPE);
  const resetActive = scopeReset || pendingScopeReset;
  const parsed = useMemo(
    () => parseCreditHistorySearch(resetActive ? '' : rawSearch, storedPageSize),
    [rawSearch, resetActive, storedPageSize],
  );

  useEffect(() => {
    if (!pendingScopeReset || rawSearch !== '') return;
    let active = true;
    queueMicrotask(() => {
      if (active) setPendingScopeReset(false);
    });
    return () => {
      active = false;
    };
  }, [pendingScopeReset, rawSearch]);

  useEffect(() => {
    if (parsed.canonicalSearch === rawSearch) return;
    setSearchParams(parsed.canonicalSearch, { replace: true });
  }, [parsed.canonicalSearch, rawSearch, setSearchParams]);

  const updateSearch = useCallback(
    (
      update: (current: CreditHistoryUrlState) => Parameters<typeof serializeHistoryValues>[0],
      replace = false,
    ) => {
      setSearchParams(
        (previous) => {
          const current = parseCreditHistorySearch(
            resetActive ? '' : previous,
            readPageSizePreference('user', CREDIT_HISTORY_LIST_TYPE),
          );
          return serializeHistoryValues(update(current));
        },
        { replace },
      );
    },
    [resetActive, setSearchParams],
  );

  const setPage = useCallback(
    (page: string, anchor?: string | null) => {
      if (!isHistoryPage(page)) return;
      const nextAnchor = anchor === undefined ? parsed.filter.anchor : (anchor ?? undefined);
      if (nextAnchor !== undefined && !isHistoryAnchor(nextAnchor)) return;
      updateSearch((current) => ({
        ...valuesFromState(current),
        page,
        pageSize: current.filter.page_size,
        includePage: true,
        includePageSize: true,
        anchor: nextAnchor,
      }));
    },
    [parsed.filter.anchor, updateSearch],
  );

  const setPageSize = useCallback(
    (pageSize: PageSize, anchor?: string | null) => {
      if (!PAGE_SIZES.includes(pageSize)) return;
      const nextAnchor = anchor === undefined ? parsed.filter.anchor : (anchor ?? undefined);
      if (nextAnchor !== undefined && !isHistoryAnchor(nextAnchor)) return;
      writePageSizePreference('user', CREDIT_HISTORY_LIST_TYPE, pageSize);
      updateSearch((current) => ({
        ...valuesFromState(current),
        page: DEFAULT_PAGE,
        pageSize,
        includePage: true,
        includePageSize: true,
        anchor: nextAnchor,
      }));
    },
    [parsed.filter.anchor, updateSearch],
  );

  const apply = useCallback(
    (draft: CreditHistoryFilterDraft): boolean => {
      if (!validDraft(draft)) return false;
      updateSearch((current) => ({
        ...valuesFromState(current),
        page: DEFAULT_PAGE,
        pageSize: current.filter.page_size,
        includePage: true,
        includePageSize: true,
        anchor: undefined,
        from: draft.from,
        to: draft.to,
        asset_type: draft.asset_type,
        category: draft.category,
        direction: draft.direction,
      }));
      return true;
    },
    [updateSearch],
  );

  const reset = useCallback(() => {
    updateSearch((current) => ({
      page: DEFAULT_PAGE,
      pageSize: current.filter.page_size,
      includePage: true,
      includePageSize: true,
    }));
  }, [updateSearch]);

  const refresh = useCallback(() => {
    setRefreshRevision((value) => value + 1);
    updateSearch(
      (current) => ({
        ...valuesFromState(current),
        page: DEFAULT_PAGE,
        pageSize: current.filter.page_size,
        includePage: true,
        includePageSize: true,
        anchor: undefined,
      }),
      true,
    );
  }, [updateSearch]);

  return {
    ...parsed,
    refreshRevision,
    setPage,
    setPageSize,
    apply,
    reset,
    refresh,
  };
}
