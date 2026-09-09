import {
  useCallback,
  useEffect,
  useMemo,
  useReducer,
  useRef,
  useState,
  type ReactNode,
} from 'react';
import { useQuery, useQueryClient, type QueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { useSearchState } from '@shared/operations/useSearchState';
import {
  captureStationSession,
  clearStationSession,
  stationSessionMatches,
  StationSessionChangedError,
  type StationSessionSnapshot,
} from '@shared/charityManagement';
import { ErrorState, EmptyState, LoadingState } from '@shared/components/States';
import { ApiError, isForbidden, isUnauthorized } from '@shared/query/http';
import { PagePagination } from '@shared/operations/PagePagination';
import { type PageMetadata, type PageSize } from '@shared/operations/pageNumbers';
import { useUrlPagePager } from '@shared/operations/useUrlPagePager';
import { getEndpoint } from '../core/api';
import { listEndpointKeysPage, listEndpointsPage } from '../core/pageApi';
import { coreSessionMatchesAccount, useCoreSession } from '../core/queries';
import type { Endpoint as CoreEndpoint, EndpointKey as CoreEndpointKey } from '../core/types';
import type { NumberedPage } from '../core/pageTypes';
import { economyKeys } from './queries';
import type {
  EndpointKeyChoice,
  EndpointKeyEligibility,
  EndpointKeySummary,
  EndpointSummary,
} from './types';
import './donationResourcePicker.css';

const MAX_SELECTED = 100;
const MAX_SEARCH_CODE_POINTS = 128;
const MAX_SEARCH_UTF16_LENGTH = 256;
const MAX_SEARCH_BYTES = 512;
const ENDPOINT_SEARCH_PARAM = 'endpoint_q';
const KEY_SEARCH_PARAM = 'key_q';
const ACTIVE_ENDPOINT_PARAM = 'endpoint_id';
const ENDPOINT_PAGE_PARAM = 'endpoint_page';
const ENDPOINT_PAGE_SIZE_PARAM = 'endpoint_page_size';
const KEY_PAGE_PARAM = 'key_page';
const KEY_PAGE_SIZE_PARAM = 'key_page_size';
const SELECTED_PAGE_PARAM = 'selected_page';
const SELECTED_PAGE_SIZE_PARAM = 'selected_page_size';
const ENDPOINT_LIST_TYPE = 'donation-resource-endpoints';
const KEY_LIST_TYPE = 'donation-resource-keys';
const SELECTED_LIST_TYPE = 'donation-resource-selected';

export interface DonationResourcePickerProps {
  accountId: string;
  selected: readonly EndpointKeyChoice[];
  onChange: (choices: EndpointKeyChoice[]) => void;
  disabled?: boolean;
  enabled?: boolean;
  renderSelected?: (choice: EndpointKeyChoice) => ReactNode;
  onReadStateChange?: (blocked: boolean) => void;
}

function invalidResponse(message: string): never {
  throw new ApiError('invalid_response', message, 200);
}

function endpointSummary(endpoint: CoreEndpoint): EndpointSummary {
  return {
    id: endpoint.id,
    connectorType: endpoint.connector_type,
    baseUrl: endpoint.base_url,
    origin:
      endpoint.origin.kind === 'custom'
        ? { kind: 'custom' }
        : {
            kind: 'mainstream',
            channelId: endpoint.origin.channel_id,
            name: endpoint.origin.name,
          },
    note: endpoint.note,
    enabled: endpoint.enabled,
    revision: endpoint.revision,
    keyCount: endpoint.key_count,
    createdAt: endpoint.created_at,
    updatedAt: endpoint.updated_at,
  };
}

function keySummary(key: CoreEndpointKey): EndpointKeySummary {
  return {
    id: key.id,
    endpointId: key.endpoint_id,
    displayHead: key.display_head,
    displayTail: key.display_tail,
    note: key.note,
    enabled: key.enabled,
    forceStoreFalse: key.force_store_false,
    maxConcurrency: key.max_concurrency,
    maxRPM: key.max_rpm,
    suspensionState: key.suspension_state,
    revision: key.revision,
    createdAt: key.created_at,
    updatedAt: key.updated_at,
  };
}

function keyChoice(endpoint: EndpointSummary, key: CoreEndpointKey): EndpointKeyChoice {
  if (key.endpoint_id !== endpoint.id) {
    invalidResponse(
      `The server returned key ${key.id} for endpoint ${key.endpoint_id} while browsing ${endpoint.id}.`,
    );
  }
  const eligibility = key.browse?.donation_eligibility;
  if (
    eligibility !== 'eligible' &&
    eligibility !== 'already_donated' &&
    eligibility !== 'security_processing'
  ) {
    invalidResponse('The server returned an endpoint key without donation eligibility.');
  }
  return { endpoint, key: keySummary(key), eligibility };
}

function validateKeyPage(
  page: NumberedPage<CoreEndpointKey>,
  endpointId: string,
): NumberedPage<CoreEndpointKey> {
  for (const key of page.data) {
    if (key.endpoint_id !== endpointId) {
      invalidResponse('The server returned an endpoint key for another endpoint.');
    }
    const eligibility = key.browse?.donation_eligibility;
    if (
      eligibility !== 'eligible' &&
      eligibility !== 'already_donated' &&
      eligibility !== 'security_processing'
    ) {
      invalidResponse('The server returned an endpoint key without donation eligibility.');
    }
  }
  return page;
}

function validSearch(value: string): boolean {
  try {
    if (new TextEncoder().encode(value).byteLength > MAX_SEARCH_BYTES) return false;
    // The core API validator rejects C0/C1 scalars and lone surrogates.
    for (const character of value) {
      const point = character.codePointAt(0) ?? 0;
      if (
        point < 0x20 ||
        (point >= 0x7f && point <= 0x9f) ||
        (point >= 0xd800 && point <= 0xdfff)
      ) {
        return false;
      }
    }
    return Array.from(value).length <= MAX_SEARCH_CODE_POINTS;
  } catch {
    return false;
  }
}

function selectedSignature(selected: readonly EndpointKeyChoice[]): string {
  return selected
    .map((choice) => `${choice.endpoint.id}\u0000${choice.key.id}`)
    .sort()
    .join('|');
}

interface SelectionScope {
  accountId: string;
  lastSignature: string;
  hiddenSignature: string | null;
}

function selectionScopeReducer(
  state: SelectionScope,
  action: { accountId: string; selectedIds: string },
): SelectionScope {
  if (state.accountId !== action.accountId) {
    return {
      accountId: action.accountId,
      lastSignature: action.selectedIds,
      hiddenSignature: state.lastSignature,
    };
  }
  if (state.lastSignature === action.selectedIds) return state;
  return {
    ...state,
    lastSignature: action.selectedIds,
    hiddenSignature: null,
  };
}

function searchDraftReducer(_state: string, action: { value: string }): string {
  return action.value;
}

function stationRead<T>(
  queryClient: QueryClient,
  accountId: string,
  request: () => Promise<T>,
): Promise<T> {
  let snapshot: StationSessionSnapshot;
  try {
    snapshot = captureStationSession(queryClient, 'steward');
  } catch {
    return Promise.reject(new StationSessionChangedError());
  }
  if (
    !coreSessionMatchesAccount(queryClient, accountId) ||
    !stationSessionMatches(queryClient, 'steward', snapshot)
  ) {
    return Promise.reject(new StationSessionChangedError());
  }
  return request().then(
    (value) => {
      if (
        !coreSessionMatchesAccount(queryClient, accountId) ||
        !stationSessionMatches(queryClient, 'steward', snapshot)
      ) {
        throw new StationSessionChangedError();
      }
      return value;
    },
    (error: unknown) => {
      if (
        !coreSessionMatchesAccount(queryClient, accountId) ||
        !stationSessionMatches(queryClient, 'steward', snapshot)
      ) {
        throw new StationSessionChangedError();
      }
      if (isUnauthorized(error) || isForbidden(error)) {
        clearStationSession(queryClient, 'steward');
      }
      throw error;
    },
  );
}

function sameEndpointPageContext(
  queryKey: readonly unknown[] | undefined,
  accountId: string,
  search: string,
  pageSize: PageSize,
): boolean {
  return (
    queryKey?.[0] === economyKeys.endpointChoicesRoot[0] &&
    queryKey?.[1] === economyKeys.endpointChoicesRoot[1] &&
    queryKey?.[2] === economyKeys.endpointChoicesRoot[2] &&
    queryKey?.[3] === accountId &&
    queryKey?.[4] === 'endpoints' &&
    queryKey?.[5] === search &&
    queryKey?.[7] === pageSize
  );
}

function sameKeyPageContext(
  queryKey: readonly unknown[] | undefined,
  accountId: string,
  endpointId: string,
  search: string,
  pageSize: PageSize,
): boolean {
  return (
    queryKey?.[0] === economyKeys.endpointChoicesRoot[0] &&
    queryKey?.[1] === economyKeys.endpointChoicesRoot[1] &&
    queryKey?.[2] === economyKeys.endpointChoicesRoot[2] &&
    queryKey?.[3] === accountId &&
    queryKey?.[4] === 'keys' &&
    queryKey?.[5] === endpointId &&
    queryKey?.[6] === search &&
    queryKey?.[8] === pageSize
  );
}

function endpointDisplayName(endpoint: EndpointSummary, fallback: string): string {
  return endpoint.note || endpoint.baseUrl || fallback;
}

function choiceDisplayKey(choice: EndpointKeyChoice): string {
  return `${choice.key.displayHead}…${choice.key.displayTail}`;
}

function eligibilityCopyKey(eligibility: EndpointKeyEligibility): string {
  return `user.charity.resourcePicker.eligibility.${eligibility}`;
}

export function DonationResourcePicker({
  accountId,
  selected,
  onChange,
  disabled = false,
  enabled = true,
  renderSelected,
  onReadStateChange,
}: DonationResourcePickerProps) {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const session = useCoreSession(false);
  const [searchParams, setSearchParams] = useSearchState();
  const [endpointSearchDraft, dispatchEndpointSearchDraft] = useReducer(
    searchDraftReducer,
    searchParams.get(ENDPOINT_SEARCH_PARAM) ?? '',
  );
  const [keySearchDraft, dispatchKeySearchDraft] = useReducer(
    searchDraftReducer,
    searchParams.get(KEY_SEARCH_PARAM) ?? '',
  );
  const [searchError, setSearchError] = useState<string | null>(null);
  const selectedIds = selectedSignature(selected);
  const [selectionScope, dispatchSelectionScope] = useReducer(selectionScopeReducer, {
    accountId,
    lastSignature: selectedIds,
    hiddenSignature: null,
  });
  const accountTransition = selectionScope.accountId !== accountId;
  const hiddenForAccountSwitch = selectionScope.hiddenSignature === selectedIds;
  const sessionData = session.data;
  const sessionAccountId = sessionData?.accountId;
  const sessionMatches =
    sessionAccountId === accountId && coreSessionMatchesAccount(queryClient, accountId);

  const previousAccountId = useRef(accountId);
  useEffect(() => {
    dispatchSelectionScope({ accountId, selectedIds });
  }, [accountId, selectedIds]);
  useEffect(() => {
    const previous = previousAccountId.current;
    if (previous === accountId) return;
    previousAccountId.current = accountId;
    queryClient.removeQueries({
      queryKey: [...economyKeys.endpointChoicesRoot, previous],
      exact: false,
    });
    setSearchParams((previousParams) => {
      const next = new URLSearchParams(previousParams);
      for (const parameter of [
        ENDPOINT_SEARCH_PARAM,
        KEY_SEARCH_PARAM,
        ACTIVE_ENDPOINT_PARAM,
        ENDPOINT_PAGE_PARAM,
        ENDPOINT_PAGE_SIZE_PARAM,
        KEY_PAGE_PARAM,
        KEY_PAGE_SIZE_PARAM,
        SELECTED_PAGE_PARAM,
        SELECTED_PAGE_SIZE_PARAM,
      ]) {
        next.delete(parameter);
      }
      return next;
    });
  }, [accountId, queryClient, setSearchParams]);

  const endpointSearch = accountTransition ? '' : (searchParams.get(ENDPOINT_SEARCH_PARAM) ?? '');
  const keySearch = accountTransition ? '' : (searchParams.get(KEY_SEARCH_PARAM) ?? '');
  const rawActiveEndpointId = accountTransition
    ? undefined
    : (searchParams.get(ACTIVE_ENDPOINT_PARAM) ?? undefined);
  const endpointSearchValid = validSearch(endpointSearch);
  const keySearchValid = validSearch(keySearch);
  const endpointPager = useUrlPagePager({
    station: 'user',
    listType: ENDPOINT_LIST_TYPE,
    scopeKey: accountId,
    scopeReady: !accountTransition && sessionMatches,
    pageParam: ENDPOINT_PAGE_PARAM,
    pageSizeParam: ENDPOINT_PAGE_SIZE_PARAM,
  });
  const keyPager = useUrlPagePager({
    station: 'user',
    listType: KEY_LIST_TYPE,
    scopeKey: accountId,
    scopeReady: !accountTransition && sessionMatches,
    pageParam: KEY_PAGE_PARAM,
    pageSizeParam: KEY_PAGE_SIZE_PARAM,
  });
  const selectedPager = useUrlPagePager({
    station: 'user',
    listType: SELECTED_LIST_TYPE,
    scopeKey: accountId,
    scopeReady: !accountTransition && sessionMatches,
    pageParam: SELECTED_PAGE_PARAM,
    pageSizeParam: SELECTED_PAGE_SIZE_PARAM,
  });
  const endpointWindow = useMemo(
    () => ({ page: endpointPager.page, pageSize: endpointPager.pageSize }),
    [endpointPager.page, endpointPager.pageSize],
  );
  const keyWindow = useMemo(
    () => ({ page: keyPager.page, pageSize: keyPager.pageSize }),
    [keyPager.page, keyPager.pageSize],
  );
  const endpointQuery = useQuery<NumberedPage<CoreEndpoint>, Error>({
    queryKey: [
      ...economyKeys.endpointChoicesRoot,
      accountId,
      'endpoints',
      endpointSearch,
      endpointPager.page,
      endpointPager.pageSize,
    ] as const,
    queryFn: ({ signal }) => {
      return stationRead(queryClient, accountId, () =>
        listEndpointsPage(endpointWindow, signal, endpointSearch),
      );
    },
    enabled: enabled && !accountTransition && sessionMatches && endpointSearchValid,
    retry: false,
    placeholderData: (previous, previousQuery) =>
      sameEndpointPageContext(
        previousQuery?.queryKey,
        accountId,
        endpointSearch,
        endpointPager.pageSize,
      )
        ? previous
        : undefined,
  });
  const visibleEndpointPage = sessionMatches && !accountTransition ? endpointQuery.data : undefined;
  const endpointRows = visibleEndpointPage?.data ?? [];
  const endpointOnPage = rawActiveEndpointId
    ? endpointRows.find((endpoint) => endpoint.id === rawActiveEndpointId)
    : undefined;
  const activeEndpointDetailQuery = useQuery<CoreEndpoint, Error>({
    queryKey: [
      ...economyKeys.endpointChoicesRoot,
      accountId,
      'endpoint-detail',
      rawActiveEndpointId ?? 'none',
    ] as const,
    queryFn: ({ signal }) => {
      if (!rawActiveEndpointId) throw new StationSessionChangedError();
      return stationRead(queryClient, accountId, async () => {
        const endpoint = await getEndpoint(rawActiveEndpointId, signal);
        if (endpoint.id !== rawActiveEndpointId) {
          invalidResponse('The server returned a different endpoint.');
        }
        return endpoint;
      });
    },
    enabled:
      enabled &&
      !accountTransition &&
      sessionMatches &&
      Boolean(rawActiveEndpointId) &&
      !endpointOnPage &&
      !endpointQuery.isPending &&
      !endpointQuery.isFetching &&
      !endpointQuery.isPlaceholderData,
    retry: false,
  });
  const visibleEndpointDetail =
    sessionMatches && !accountTransition ? activeEndpointDetailQuery.data : undefined;
  const detailDependsOnRequest = Boolean(rawActiveEndpointId && !endpointOnPage);
  const accountScopedSelected = useMemo(
    () => (sessionMatches && !accountTransition && !hiddenForAccountSwitch ? selected : []),
    [accountTransition, hiddenForAccountSwitch, selected, sessionMatches],
  );
  const activeEndpoint = endpointOnPage
    ? endpointSummary(endpointOnPage)
    : visibleEndpointDetail
      ? endpointSummary(visibleEndpointDetail)
      : accountScopedSelected.find((choice) => choice.endpoint.id === rawActiveEndpointId)
          ?.endpoint;
  const activeEndpointReady = Boolean(endpointOnPage || visibleEndpointDetail);
  const activeEndpointTitle = activeEndpoint
    ? endpointDisplayName(activeEndpoint, rawActiveEndpointId ?? activeEndpoint.id)
    : (rawActiveEndpointId ?? '');
  const selectedTotalPages = Math.max(
    1,
    Math.ceil(accountScopedSelected.length / selectedPager.pageSize),
  );
  const selectedRequestedPage = BigInt(selectedPager.page);
  const selectedActualPage =
    selectedRequestedPage > BigInt(selectedTotalPages)
      ? String(selectedTotalPages)
      : selectedPager.page;
  const selectedPageOffset = Number(BigInt(selectedActualPage) - 1n) * selectedPager.pageSize;
  const selectedRows = accountScopedSelected.slice(
    selectedPageOffset,
    selectedPageOffset + selectedPager.pageSize,
  );
  const selectedMetadata: PageMetadata = {
    page: selectedActualPage,
    page_size: selectedPager.pageSize,
    total_items: String(accountScopedSelected.length),
    total_pages: String(selectedTotalPages),
  };
  const keyQuery = useQuery<NumberedPage<CoreEndpointKey>, Error>({
    queryKey: [
      ...economyKeys.endpointChoicesRoot,
      accountId,
      'keys',
      rawActiveEndpointId ?? 'none',
      keySearch,
      keyPager.page,
      keyPager.pageSize,
    ] as const,
    queryFn: ({ signal }) => {
      if (!rawActiveEndpointId || !activeEndpoint) {
        throw new StationSessionChangedError();
      }
      return stationRead(queryClient, accountId, () =>
        listEndpointKeysPage(rawActiveEndpointId, keyWindow, signal, keySearch),
      ).then((page) => validateKeyPage(page, rawActiveEndpointId));
    },
    enabled:
      enabled &&
      !accountTransition &&
      sessionMatches &&
      Boolean(rawActiveEndpointId) &&
      activeEndpointReady &&
      keySearchValid,
    retry: false,
    placeholderData: (previous, previousQuery) =>
      rawActiveEndpointId &&
      sameKeyPageContext(
        previousQuery?.queryKey,
        accountId,
        rawActiveEndpointId,
        keySearch,
        keyPager.pageSize,
      )
        ? previous
        : undefined,
  });
  const visibleKeyPage = sessionMatches && !accountTransition ? keyQuery.data : undefined;
  const keyChoices =
    visibleKeyPage && activeEndpoint
      ? visibleKeyPage.data.map((key) => keyChoice(activeEndpoint, key))
      : [];
  useEffect(() => {
    if (
      !sessionMatches ||
      accountTransition ||
      !keyQuery.isSuccess ||
      keyQuery.isFetching ||
      keyQuery.isPlaceholderData ||
      accountScopedSelected.length === 0
    ) {
      return;
    }
    if (!visibleKeyPage || !activeEndpoint) return;
    const freshChoices = visibleKeyPage.data.map((key) => keyChoice(activeEndpoint, key));
    if (freshChoices.length === 0) return;
    const freshById = new Map(freshChoices.map((choice) => [choice.key.id, choice]));
    let changed = false;
    const merged = accountScopedSelected.map((choice) => {
      const fresh = freshById.get(choice.key.id);
      if (
        !fresh ||
        (choice.eligibility === fresh.eligibility &&
          choice.endpoint.revision === fresh.endpoint.revision &&
          choice.key.revision === fresh.key.revision)
      ) {
        return choice;
      }
      changed = true;
      // Keep parent-owned fields (such as expiry details) while refreshing
      // the authority fields represented by the current key page.
      return { ...choice, ...fresh };
    });
    if (changed) onChange(merged);
  }, [
    accountScopedSelected,
    accountTransition,
    activeEndpoint,
    keyQuery.isFetching,
    keyQuery.isPlaceholderData,
    keyQuery.isSuccess,
    onChange,
    sessionMatches,
    visibleKeyPage,
  ]);
  const currentKeyById = new Map(keyChoices.map((choice) => [choice.key.id, choice]));
  const invalidSelectedIds = accountScopedSelected
    .filter((choice) => {
      if (choice.eligibility !== 'eligible') return true;
      const current = currentKeyById.get(choice.key.id);
      return current !== undefined && current.eligibility !== 'eligible';
    })
    .map((choice) => choice.key.id);
  const selectionBlocked = invalidSelectedIds.length > 0;
  const endpointBusy =
    enabled &&
    (endpointQuery.isPending || endpointQuery.isFetching || endpointQuery.isPlaceholderData);
  const detailBusy =
    detailDependsOnRequest &&
    (activeEndpointDetailQuery.isPending || activeEndpointDetailQuery.isFetching);
  const keyBusy =
    enabled &&
    Boolean(rawActiveEndpointId) &&
    (keyQuery.isPending || keyQuery.isFetching || keyQuery.isPlaceholderData);
  const readBlocked =
    enabled &&
    (accountTransition ||
      !sessionMatches ||
      !endpointSearchValid ||
      endpointBusy ||
      Boolean(endpointQuery.error) ||
      detailBusy ||
      (detailDependsOnRequest && Boolean(activeEndpointDetailQuery.error)) ||
      !keySearchValid ||
      keyBusy ||
      Boolean(keyQuery.error) ||
      selectionBlocked);
  useEffect(() => {
    onReadStateChange?.(readBlocked);
  }, [onReadStateChange, readBlocked]);

  useEffect(() => {
    dispatchEndpointSearchDraft({ value: endpointSearch });
  }, [endpointSearch]);
  useEffect(() => {
    dispatchKeySearchDraft({ value: keySearch });
  }, [keySearch]);

  const commitSearch = useCallback(
    (parameter: string, pageParameter: string, value: string): boolean => {
      if (!enabled) return false;
      if (!validSearch(value)) {
        setSearchError(t('user.charity.resourcePicker.invalidSearch'));
        return false;
      }
      setSearchError(null);
      setSearchParams((previous) => {
        const next = new URLSearchParams(previous);
        if (value) next.set(parameter, value);
        else next.delete(parameter);
        next.delete(pageParameter);
        next.set(pageParameter, '1');
        return next;
      });
      return true;
    },
    [enabled, setSearchParams, t],
  );

  const selectEndpoint = useCallback(
    (endpointId: string) => {
      if (!enabled || disabled || endpointBusy || !sessionMatches) return;
      setSearchParams((previous) => {
        const next = new URLSearchParams(previous);
        next.set(ACTIVE_ENDPOINT_PARAM, endpointId);
        next.delete(KEY_PAGE_PARAM);
        next.set(KEY_PAGE_PARAM, '1');
        return next;
      });
    },
    [disabled, enabled, endpointBusy, sessionMatches, setSearchParams],
  );

  const closeEndpoint = useCallback(() => {
    if (!enabled || disabled) return;
    setSearchParams((previous) => {
      const next = new URLSearchParams(previous);
      next.delete(ACTIVE_ENDPOINT_PARAM);
      next.delete(KEY_SEARCH_PARAM);
      next.delete(KEY_PAGE_PARAM);
      next.delete(KEY_PAGE_SIZE_PARAM);
      return next;
    });
  }, [disabled, enabled, setSearchParams]);

  const chooseKey = useCallback(
    (choice: EndpointKeyChoice, checked: boolean) => {
      if (!enabled || disabled || keyBusy || !sessionMatches) return;
      const current = accountScopedSelected;
      const existing = current.some((item) => item.key.id === choice.key.id);
      if (checked) {
        if (existing || choice.eligibility !== 'eligible' || current.length >= MAX_SELECTED) return;
        onChange([...current, choice]);
      } else if (existing) {
        onChange(current.filter((item) => item.key.id !== choice.key.id));
      }
    },
    [accountScopedSelected, disabled, enabled, keyBusy, onChange, sessionMatches],
  );

  const removeSelected = useCallback(
    (keyId: string) => {
      if (!enabled || disabled || !sessionMatches) return;
      onChange(accountScopedSelected.filter((choice) => choice.key.id !== keyId));
    },
    [accountScopedSelected, disabled, enabled, onChange, sessionMatches],
  );

  const endpointSearchForm = (
    <form
      className="donation-resource-picker__search"
      onSubmit={(event) => {
        event.preventDefault();
        commitSearch(ENDPOINT_SEARCH_PARAM, ENDPOINT_PAGE_PARAM, endpointSearchDraft);
      }}
    >
      <label>
        <span>{t('user.charity.resourcePicker.endpointSearch')}</span>
        <input
          type="search"
          value={endpointSearchDraft}
          maxLength={MAX_SEARCH_UTF16_LENGTH}
          placeholder={t('user.charity.resourcePicker.endpointSearchPlaceholder')}
          disabled={!enabled || disabled}
          onChange={(event) => dispatchEndpointSearchDraft({ value: event.target.value })}
        />
      </label>
      <button type="submit" className="btn btn-secondary" disabled={!enabled || disabled}>
        {t('common.search')}
      </button>
      <button
        type="button"
        className="btn btn-quiet"
        disabled={!enabled || disabled || (!endpointSearchDraft && !endpointSearch)}
        onClick={() => {
          dispatchEndpointSearchDraft({ value: '' });
          commitSearch(ENDPOINT_SEARCH_PARAM, ENDPOINT_PAGE_PARAM, '');
        }}
      >
        {t('common.resetFilter')}
      </button>
    </form>
  );

  const keySearchForm = (
    <form
      className="donation-resource-picker__search"
      onSubmit={(event) => {
        event.preventDefault();
        commitSearch(KEY_SEARCH_PARAM, KEY_PAGE_PARAM, keySearchDraft);
      }}
    >
      <label>
        <span>{t('user.charity.resourcePicker.keySearch')}</span>
        <input
          type="search"
          value={keySearchDraft}
          maxLength={MAX_SEARCH_UTF16_LENGTH}
          placeholder={t('user.charity.resourcePicker.keySearchPlaceholder')}
          disabled={!enabled || disabled}
          onChange={(event) => dispatchKeySearchDraft({ value: event.target.value })}
        />
      </label>
      <button type="submit" className="btn btn-secondary" disabled={!enabled || disabled}>
        {t('common.search')}
      </button>
      <button
        type="button"
        className="btn btn-quiet"
        disabled={!enabled || disabled || (!keySearchDraft && !keySearch)}
        onClick={() => {
          dispatchKeySearchDraft({ value: '' });
          commitSearch(KEY_SEARCH_PARAM, KEY_PAGE_PARAM, '');
        }}
      >
        {t('common.resetFilter')}
      </button>
    </form>
  );

  return (
    <div className="donation-resource-picker" aria-busy={readBlocked}>
      <div className="donation-resource-picker__header">
        <div>
          <h3>{t('user.charity.resourcePicker.title')}</h3>
          <p>{t('user.charity.resourcePicker.description')}</p>
        </div>
        <span className="donation-resource-picker__count">
          {t('user.charity.resourcePicker.selectedCount', {
            count: accountScopedSelected.length,
            max: MAX_SELECTED,
          })}
        </span>
      </div>
      {searchError ? (
        <p className="field-error" role="alert">
          {searchError}
        </p>
      ) : null}
      {selectionBlocked ? (
        <p className="inline-notice economy-notice economy-notice--warning" role="alert">
          {t('user.charity.resourcePicker.selectedUnavailable')}
        </p>
      ) : null}
      <section className="donation-resource-picker__section">
        <div className="donation-resource-picker__section-heading">
          <div>
            <h4>{t('user.charity.resourcePicker.endpointTitle')}</h4>
            <p>{t('user.charity.resourcePicker.endpointDescription')}</p>
          </div>
          {rawActiveEndpointId ? (
            <button
              type="button"
              className="btn btn-quiet"
              disabled={!enabled || disabled}
              onClick={closeEndpoint}
            >
              {t('user.charity.resourcePicker.backToEndpoints')}
            </button>
          ) : null}
        </div>
        {endpointSearchForm}
        {!endpointSearchValid ? (
          <p className="field-error" role="alert">
            {t('user.charity.resourcePicker.invalidSearch')}
          </p>
        ) : endpointQuery.isPending && !visibleEndpointPage ? (
          <LoadingState />
        ) : endpointQuery.error ? (
          <ErrorState error={endpointQuery.error} onRetry={() => void endpointQuery.refetch()} />
        ) : visibleEndpointPage && endpointRows.length === 0 ? (
          <>
            <EmptyState
              title={t('user.charity.resourcePicker.noEndpoints')}
              body={t('user.charity.resourcePicker.noEndpointsBody')}
            />
            <PagePagination
              metadata={visibleEndpointPage.pagination}
              requestedPage={endpointPager.page}
              busy={!enabled || disabled || endpointBusy}
              onPageChange={endpointPager.setPage}
              onPageSizeChange={(size: PageSize) => endpointPager.setPageSize(size)}
            />
          </>
        ) : visibleEndpointPage ? (
          <>
            <ul
              className="donation-resource-picker__endpoint-list"
              aria-label={t('user.charity.resourcePicker.endpointTitle')}
            >
              {endpointRows.map((endpoint) => {
                const summary = endpointSummary(endpoint);
                const selectedHere = rawActiveEndpointId === endpoint.id;
                return (
                  <li key={endpoint.id} className={selectedHere ? 'is-selected' : undefined}>
                    <button
                      type="button"
                      className="donation-resource-picker__endpoint"
                      disabled={!enabled || disabled || endpointBusy || !sessionMatches}
                      aria-expanded={selectedHere}
                      onClick={() => selectEndpoint(endpoint.id)}
                    >
                      <span>
                        <strong>
                          {endpointDisplayName(
                            summary,
                            t('user.charity.resourcePicker.unnamedEndpoint'),
                          )}
                        </strong>
                        <small>{summary.baseUrl}</small>
                        <small>
                          {t('user.charity.resourcePicker.endpointKeys', {
                            count: summary.keyCount,
                          })}
                          {' · '}
                          {summary.enabled ? t('common.enabled') : t('common.disabled')}
                        </small>
                      </span>
                      <span aria-hidden="true">›</span>
                    </button>
                  </li>
                );
              })}
            </ul>
            <PagePagination
              metadata={visibleEndpointPage.pagination}
              requestedPage={endpointPager.page}
              busy={!enabled || disabled || endpointBusy}
              onPageChange={endpointPager.setPage}
              onPageSizeChange={(size: PageSize) => endpointPager.setPageSize(size)}
            />
          </>
        ) : (
          <LoadingState />
        )}
      </section>
      {rawActiveEndpointId ? (
        <section className="donation-resource-picker__section">
          <div className="donation-resource-picker__section-heading">
            <div>
              <h4>
                {t('user.charity.resourcePicker.keyTitle', {
                  endpoint: activeEndpointTitle,
                })}
              </h4>
              {activeEndpoint ? <p>{activeEndpoint.baseUrl}</p> : null}
            </div>
          </div>
          {detailDependsOnRequest && activeEndpointDetailQuery.error ? (
            <ErrorState
              error={activeEndpointDetailQuery.error}
              onRetry={() => void activeEndpointDetailQuery.refetch()}
            />
          ) : detailBusy || !activeEndpointReady ? (
            <LoadingState />
          ) : (
            <>
              {keySearchForm}
              {!keySearchValid ? (
                <p className="field-error" role="alert">
                  {t('user.charity.resourcePicker.invalidSearch')}
                </p>
              ) : keyQuery.isPending && !visibleKeyPage ? (
                <LoadingState />
              ) : keyQuery.error ? (
                <ErrorState error={keyQuery.error} onRetry={() => void keyQuery.refetch()} />
              ) : visibleKeyPage && keyChoices.length === 0 ? (
                <>
                  <EmptyState
                    title={t('user.charity.resourcePicker.noKeys')}
                    body={t('user.charity.resourcePicker.noKeysBody')}
                  />
                  <PagePagination
                    metadata={visibleKeyPage.pagination}
                    requestedPage={keyPager.page}
                    busy={!enabled || disabled || keyBusy}
                    onPageChange={keyPager.setPage}
                    onPageSizeChange={(size: PageSize) => keyPager.setPageSize(size)}
                  />
                </>
              ) : visibleKeyPage ? (
                <>
                  {accountScopedSelected.length >= MAX_SELECTED ? (
                    <p className="inline-notice" role="status">
                      {t('user.charity.resourcePicker.selectionLimit', { max: MAX_SELECTED })}
                    </p>
                  ) : null}
                  <ul
                    className="donation-resource-picker__key-list"
                    aria-label={t('user.charity.resourcePicker.keyTitle', {
                      endpoint: activeEndpointTitle,
                    })}
                  >
                    {keyChoices.map((choice) => {
                      const selectedHere = accountScopedSelected.some(
                        (item) => item.key.id === choice.key.id,
                      );
                      const unavailable = choice.eligibility !== 'eligible';
                      const rowDisabled =
                        disabled ||
                        keyBusy ||
                        unavailable ||
                        (!selectedHere && accountScopedSelected.length >= MAX_SELECTED);
                      return (
                        <li
                          key={choice.key.id}
                          className={unavailable ? 'is-unavailable' : undefined}
                        >
                          <label className="donation-resource-picker__key">
                            <input
                              type="checkbox"
                              checked={selectedHere}
                              disabled={!enabled || rowDisabled}
                              onChange={(event) => chooseKey(choice, event.target.checked)}
                            />
                            <span>
                              <strong>{choice.key.note || choiceDisplayKey(choice)}</strong>
                              <code>{choiceDisplayKey(choice)}</code>
                              <small>
                                {t('user.charity.resourcePicker.keyLimits', {
                                  concurrency: choice.key.maxConcurrency,
                                  rpm: choice.key.maxRPM,
                                })}
                              </small>
                              {!choice.key.enabled ? (
                                <small>{t('user.charity.resourcePicker.physicalDisabled')}</small>
                              ) : null}
                              {unavailable ? (
                                <small>{t(eligibilityCopyKey(choice.eligibility))}</small>
                              ) : null}
                            </span>
                          </label>
                        </li>
                      );
                    })}
                  </ul>
                  <PagePagination
                    metadata={visibleKeyPage.pagination}
                    requestedPage={keyPager.page}
                    busy={!enabled || disabled || keyBusy}
                    onPageChange={keyPager.setPage}
                    onPageSizeChange={(size: PageSize) => keyPager.setPageSize(size)}
                  />
                </>
              ) : (
                <LoadingState />
              )}
            </>
          )}
        </section>
      ) : null}
      <section
        className="donation-resource-picker__selected"
        aria-label={t('user.charity.resourcePicker.selectedTitle')}
      >
        <div className="donation-resource-picker__section-heading">
          <div>
            <h4>{t('user.charity.resourcePicker.selectedTitle')}</h4>
            <p>{t('user.charity.resourcePicker.selectedDescription')}</p>
          </div>
        </div>
        {accountScopedSelected.length === 0 ? (
          <p>{t('user.charity.resourcePicker.noSelected')}</p>
        ) : (
          <>
            <ul className="donation-resource-picker__selected-list">
              {selectedRows.map((choice) => (
                <li key={choice.key.id}>
                  <div>
                    <strong>{choice.key.note || choiceDisplayKey(choice)}</strong>
                    <small>{endpointDisplayName(choice.endpoint, choice.endpoint.id)}</small>
                    <code>{choiceDisplayKey(choice)}</code>
                    {renderSelected ? (
                      <div className="donation-resource-picker__selected-slot">
                        {renderSelected(choice)}
                      </div>
                    ) : null}
                  </div>
                  <button
                    type="button"
                    className="btn btn-quiet"
                    disabled={!enabled || disabled || !sessionMatches}
                    onClick={() => removeSelected(choice.key.id)}
                  >
                    {t('common.operations.charity.remove')}
                  </button>
                </li>
              ))}
            </ul>
            <PagePagination
              metadata={selectedMetadata}
              requestedPage={selectedPager.page}
              busy={!enabled || disabled || accountTransition}
              onPageChange={selectedPager.setPage}
              onPageSizeChange={(size: PageSize) => selectedPager.setPageSize(size)}
            />
          </>
        )}
      </section>
    </div>
  );
}
