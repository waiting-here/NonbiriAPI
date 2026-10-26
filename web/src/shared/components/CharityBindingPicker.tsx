import { NativeServicesField } from './NativeServicesField';
import type { NativeService } from '@shared/nativeServices';
import { smartBackoffDeadline } from '@shared/operations/smartBackoff';
import { Button } from '@shared/components/ui/Button';
import { Field } from './ui/Field';
import { KeyStateFilter } from './charity/KeyStateFilter';
import type { KeyStateFilter as KeyState } from '@shared/operations/keyState';
import { useEffect, useRef, useState, type ReactNode } from 'react';
import { useQuery, useQueryClient, type QueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import type { TFunction } from 'i18next';
import {
  useManagementCapability,
  captureStationSession,
  stationSessionMatches,
  clearStationSession,
  StationSessionChangedError,
} from '@shared/charityManagement';
import { isForbidden, isUnauthorized } from '@shared/query/http';
import {
  getDonationSourceKeysPage,
  getDonationSourcesPage,
  type DonationPageSafeSource,
  type DonationSourceSummary,
  type ManagedDonationKeySummary,
} from '@shared/operations/donationPages';
import { useDetailNavigation } from '@shared/operations/useDetailNavigation';
import { PagePagination } from '@shared/operations/PagePagination';
import {
  getManagedBindingCandidatesPage,
  type CharityBindingCandidatePageFilters,
} from '@shared/operations/charityPages';
import { validManagementSearch } from '@shared/operations/charityModelPages';
import { usePagePager } from '@shared/operations/usePagePager';
import {
  charityKeys,
  type CharityBindingCandidate,
  type CharityRole,
} from '@shared/operations/charity';
import type { PageMetadata } from '@shared/operations/pageNumbers';
import { EmptyState, ErrorState, LoadingState } from './States';
import { KeyLimitSummary } from './KeyRoutingLimits';
import { KeyRateLimitTime } from './KeyRateLimitTime';
import { useRateLimitRefresh } from '@shared/operations/useRateLimitRefresh';
import { DonationKeyNotes } from './DonationControlFacts';
import { DonationDiscoveryControl } from './DonationDiscoveryControl';
import './CharityBindingPicker.css';

export type CharitySelection = CharityBindingCandidate & {
  note: string;
  selected_native_services?: NativeService[];
};

const sourcePageList = 'charity-binding-sources';
const sourceKeyPageList = 'charity-binding-source-keys';
const candidatePageList = 'charity-binding-candidates';

type SourceScope = 'active' | 'all';
type CandidateSource = '' | 'automatic' | 'manual';

const charitySelectionKey = (entry: CharityBindingCandidate) =>
  `${entry.donation_key_id}:${entry.upstream_model_id}`;

const sourceTypeKey = (source: 'automatic' | 'manual') =>
  `common.operations.charity.sourceType.${source}`;

function sessionAccount(role: CharityRole, value: unknown): string | undefined {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) return undefined;
  const root = value as Record<string, unknown>;
  if (role === 'admin') {
    const admin = root.admin;
    if (admin === null || typeof admin !== 'object' || Array.isArray(admin)) return undefined;
    const username = (admin as Record<string, unknown>).username;
    return typeof username === 'string' && username.length > 0 ? username : undefined;
  }
  const user = root.user;
  if (user === null || typeof user !== 'object' || Array.isArray(user)) return undefined;
  const userRoot = user as Record<string, unknown>;
  const id = userRoot.id;
  const level = userRoot.effective_level;
  return typeof id === 'string' && id.length > 0 && (level === 5 || level === 6) ? id : undefined;
}

function sourceTitle(source: DonationPageSafeSource, translate: TFunction): string {
  return source.kind === 'mainstream'
    ? translate('common.operations.charity.mainstreamSource', { name: source.name })
    : translate('common.operations.charity.customSource');
}

async function guardedRead<T>(
  client: QueryClient,
  role: CharityRole,
  account: string | undefined,
  request: () => Promise<T>,
): Promise<T> {
  const frame = role === 'admin' ? 'admin' : 'steward';
  const snapshot = captureStationSession(client, frame);
  if (
    !account ||
    sessionAccount(
      role,
      client.getQueryData(role === 'admin' ? ['admin', 'session'] : ['user', 'session']),
    ) !== account
  ) {
    throw new StationSessionChangedError();
  }
  try {
    const result = await request();
    if (!stationSessionMatches(client, frame, snapshot)) throw new StationSessionChangedError();
    return result;
  } catch (error) {
    if (!stationSessionMatches(client, frame, snapshot)) throw new StationSessionChangedError();
    if (isUnauthorized(error)) clearStationSession(client, frame);
    throw error;
  }
}

function sourceBaseURL(source: DonationPageSafeSource): string {
  return source.base_url;
}

function isTerminalKey(key: ManagedDonationKeySummary): boolean {
  return key.charity_state === 'ended' || key.charity_state === 'expired';
}

function sourcePageFilters(query: string, scope: SourceScope, key_state: KeyState) {
  return { q: query || undefined, scope, key_state } as const;
}

function sourceKeyPageFilters(query: string, scope: SourceScope, key_state: KeyState) {
  return { q: query || undefined, scope, key_state } as const;
}

function candidatePageFilters(
  key: ManagedDonationKeySummary,
  query: string,
  source: CandidateSource,
  key_state: KeyState,
): CharityBindingCandidatePageFilters {
  return {
    include_bound: true,
    donation_id: key.donation_id,
    donation_key_id: key.key_id,
    q: query || undefined,
    source: source || undefined,
    key_state,
  };
}

function pagePlaceholder<T>(
  previous: T | undefined,
  previousQuery: { queryKey: readonly unknown[] } | undefined,
  identity: readonly unknown[],
  page: string,
  pageSize: number,
): T | undefined {
  if (!previous || !previousQuery) return undefined;
  const previousKey = previousQuery.queryKey;
  if (
    previousKey.length !== identity.length + 2 ||
    previousKey[identity.length] === page ||
    previousKey[identity.length + 1] !== pageSize
  ) {
    return undefined;
  }
  for (let index = 0; index < identity.length; index += 1) {
    if (previousKey[index] !== identity[index]) return undefined;
  }
  return previous;
}

export interface CharityPickerKeyManagerProps {
  modelID: string;
  donationID: string;
  keyID: string;
  onBack: () => void;
  backLabel?: string;
}

interface CharityBindingPickerProps {
  showSelectionSummary?: boolean;
  fixedKey?: ManagedDonationKeySummary;
  capacity?: number;
  role: CharityRole;
  modelId: string;
  selected: Record<string, CharitySelection>;
  onChange: (next: Record<string, CharitySelection>) => void;
  locked: boolean;
  onCapabilityLoss?: () => void;
  onReadStateChange?: (blocked: boolean) => void;
  onInspectBindings?: () => void;
  renderKeyManager?: (props: CharityPickerKeyManagerProps) => ReactNode;
}

type ManagementCapability = ReturnType<typeof useManagementCapability>;

type SessionProjection = {
  admin?: { username?: string };
  user?: { id?: string; effective_level?: number };
};

export function CharityBindingPicker(props: CharityBindingPickerProps) {
  const { onChange } = props;
  const management = useManagementCapability(props.role);
  const session = useQuery<SessionProjection | null>({
    queryKey: props.role === 'admin' ? ['admin', 'session'] : ['user', 'session'],
    enabled: false,
    retry: false,
  });
  const account = sessionAccount(props.role, session.data);
  const identity = `${props.role}:${props.modelId}:${props.fixedKey?.key_id ?? 'any-key'}:${account ?? 'none'}`;
  const [selectionScope, setSelectionScope] = useState<{
    identity: string;
    previousSelection?: Record<string, CharitySelection>;
  }>({ identity });
  if (selectionScope.identity !== identity)
    setSelectionScope({ identity, previousSelection: props.selected });
  useEffect(() => {
    if (selectionScope.previousSelection === props.selected) onChange({});
  }, [selectionScope.previousSelection, props.selected, onChange]);
  return (
    <CharityBindingPickerBody
      key={identity}
      {...props}
      selected={
        selectionScope.identity === identity && selectionScope.previousSelection !== props.selected
          ? props.selected
          : {}
      }
      account={account}
      management={management}
      sessionError={session.error}
    />
  );
}

function CharityBindingPickerBody({
  showSelectionSummary = true,
  role,
  modelId,
  selected,
  onChange,
  locked,
  onCapabilityLoss,
  onReadStateChange,
  onInspectBindings,
  renderKeyManager,
  account,
  management,
  sessionError,
  fixedKey,
  capacity = 256,
}: CharityBindingPickerProps & {
  account: string | undefined;
  management: ManagementCapability;
  sessionError: unknown;
}) {
  const { t } = useTranslation();
  const client = useQueryClient();
  const [keyState, setKeyState] = useState<KeyState>('all');
  const [selectedSearch, setSelectedSearch] = useState('');
  const selectedSearchInput = useRef<HTMLInputElement>(null);
  const [scope, setScope] = useState<SourceScope>('all');
  const [sourceQueryDraft, setSourceQueryDraft] = useState('');
  const [sourceQuery, setSourceQuery] = useState('');
  const [selectedSource, setSelectedSource] = useState<DonationSourceSummary | null>(() =>
    fixedKey
      ? {
          source_key: '',
          safe_source: fixedKey.safe_source,
          donation_count: '1',
          key_count: '1',
          usable_key_count: fixedKey.charity_state === 'available' ? '1' : '0',
          pending_donation_count: '0',
        }
      : null,
  );
  const [keyQueryDraft, setKeyQueryDraft] = useState('');
  const [keyQuery, setKeyQuery] = useState('');
  const [selectedKeySnapshot, setSelectedKey] = useState<ManagedDonationKeySummary | null>(
    fixedKey ?? null,
  );
  const [candidateQueryDraft, setCandidateQueryDraft] = useState('');
  const [candidateQuery, setCandidateQuery] = useState('');
  const [candidateSource, setCandidateSource] = useState<CandidateSource>('');
  const lossNotified = useRef(false);
  const [managingKey, setManagingKey] = useState(false);
  const { listRef, detailRef, remember } = useDetailNavigation(
    managingKey ? (selectedKeySnapshot?.key_id ?? '') : '',
  );

  const station = role === 'admin' ? 'admin' : 'user';
  const sourcePager = usePagePager({
    station,
    listType: sourcePageList,
    scopeKey: account ?? 'no-account',
    resetKey: `${scope}:${sourceQuery}:${keyState}`,
  });
  const sourceKeyPager = usePagePager({
    station,
    listType: sourceKeyPageList,
    scopeKey: `${account ?? 'no-account'}:${selectedSource?.source_key ?? 'no-source'}`,
    resetKey: `${scope}:${keyQuery}:${keyState}`,
  });
  const candidatePager = usePagePager({
    station,
    listType: candidatePageList,
    scopeKey: `${account ?? 'no-account'}:${modelId}:${selectedKeySnapshot?.key_id ?? 'no-key'}`,
    resetKey: `${candidateQuery}:${candidateSource}`,
  });
  const selectedPager = usePagePager({
    station,
    listType: 'charity-binding-selected',
    scopeKey: `${account ?? 'no-account'}:${modelId}`,
    resetKey: selectedSearch,
  });

  const sessionCapabilityLost = management.data === true;
  const sessionErrorLost = isUnauthorized(sessionError) || isForbidden(sessionError);
  const canRead =
    Boolean(account) && management.authorityReady && !sessionCapabilityLost && !sessionError;
  const readEnabled = canRead && !managingKey;
  const sourceQueryIdentity = [
    ...charityKeys.root(role),
    'binding-picker',
    account ?? 'no-account',
    modelId,
    'sources',
    scope,
    sourceQuery,
    keyState,
  ] as const;
  const sourceQueryKey = [...sourceQueryIdentity, sourcePager.page, sourcePager.pageSize] as const;
  const sourceKeyQueryIdentity = [
    ...charityKeys.root(role),
    'binding-picker',
    account ?? 'no-account',
    modelId,
    'source-keys',
    selectedSource?.source_key ?? 'no-source',
    scope,
    keyQuery,
    keyState,
  ] as const;
  const sourceKeyQueryKey = [
    ...sourceKeyQueryIdentity,
    sourceKeyPager.page,
    sourceKeyPager.pageSize,
  ] as const;
  const candidateQueryIdentity = [
    ...charityKeys.root(role),
    'binding-picker',
    account ?? 'no-account',
    modelId,
    'candidates',
    selectedSource?.source_key ?? 'no-source',
    selectedKeySnapshot?.key_id ?? 'no-key',
    candidateQuery,
    candidateSource,
    keyState,
  ] as const;
  const candidateQueryKey = [
    ...candidateQueryIdentity,
    candidatePager.page,
    candidatePager.pageSize,
  ] as const;
  const sources = useQuery({
    queryKey: sourceQueryKey,
    queryFn: ({ signal }) =>
      guardedRead(client, role, account, () =>
        getDonationSourcesPage(
          role,
          sourcePageFilters(sourceQuery, scope, keyState),
          sourcePager.page,
          sourcePager.pageSize,
          signal,
          role === 'steward' ? modelId : undefined,
        ),
      ),
    enabled: readEnabled && !fixedKey,
    placeholderData: (previous, previousQuery) =>
      pagePlaceholder(
        previous,
        previousQuery,
        sourceQueryIdentity,
        sourcePager.page,
        sourcePager.pageSize,
      ),
    retry: false,
  });
  const sourceKeys = useQuery({
    queryKey: sourceKeyQueryKey,
    queryFn: ({ signal }) =>
      guardedRead(client, role, account, () =>
        getDonationSourceKeysPage(
          role,
          selectedSource?.source_key ?? '',
          sourceKeyPageFilters(keyQuery, scope, keyState),
          sourceKeyPager.page,
          sourceKeyPager.pageSize,
          signal,
          role === 'steward' ? modelId : undefined,
        ),
      ),
    enabled: readEnabled && Boolean(selectedSource) && !fixedKey,
    placeholderData: (previous, previousQuery) =>
      pagePlaceholder(
        previous,
        previousQuery,
        sourceKeyQueryIdentity,
        sourceKeyPager.page,
        sourceKeyPager.pageSize,
      ),
    retry: false,
  });
  const selectedKey =
    sourceKeys.data?.data.find((entry) => entry.key_id === selectedKeySnapshot?.key_id) ??
    selectedKeySnapshot;
  useRateLimitRefresh(
    readEnabled
      ? (sourceKeys.data?.data
          .filter((key) => key.charity_state === 'rate_limited')
          .flatMap((key) => [key.rate_limited_until, smartBackoffDeadline(key.smart_backoff)]) ??
          [])
      : [],
    () => sourceKeys.refetch({ throwOnError: true }),
  );
  const candidates = useQuery({
    queryKey: candidateQueryKey,
    queryFn: ({ signal }) =>
      guardedRead(client, role, account, () =>
        getManagedBindingCandidatesPage(
          role,
          modelId,
          candidatePageFilters(
            selectedKey!,
            candidateQuery,
            candidateSource,
            fixedKey ? 'all' : keyState,
          ),
          candidatePager.page,
          candidatePager.pageSize,
          signal,
        ),
      ),
    enabled: readEnabled && Boolean(selectedSource && selectedKey),
    placeholderData: (previous, previousQuery) =>
      pagePlaceholder(
        previous,
        previousQuery,
        candidateQueryIdentity,
        candidatePager.page,
        candidatePager.pageSize,
      ),
    retry: false,
  });

  const requestErrors = [sessionError, sources.error, sourceKeys.error, candidates.error];
  const requestCapabilityLost = requestErrors.some(
    (error) => isUnauthorized(error) || isForbidden(error),
  );
  useEffect(() => {
    if (!sessionCapabilityLost && !requestCapabilityLost) return;
    if (lossNotified.current) return;
    lossNotified.current = true;
    onCapabilityLoss?.();
  }, [onCapabilityLoss, requestCapabilityLost, sessionCapabilityLost]);

  const sourceReadActive = canRead && !fixedKey;
  const sourceKeyReadActive = canRead && Boolean(selectedSource) && !fixedKey;
  const candidateReadActive = canRead && Boolean(selectedSource && selectedKey);
  const sourceReadBlocked =
    sourceReadActive && (sources.isPending || sources.isFetching || Boolean(sources.error));
  const sourceKeyReadBlocked =
    sourceKeyReadActive &&
    (sourceKeys.isPending || sourceKeys.isFetching || Boolean(sourceKeys.error));
  const candidateReadBlocked =
    candidateReadActive &&
    (candidates.isPending || candidates.isFetching || Boolean(candidates.error));
  const readStateBlocked = !canRead || managingKey || requestCapabilityLost;
  useEffect(() => {
    onReadStateChange?.(readStateBlocked);
  }, [onReadStateChange, readStateBlocked]);
  useEffect(() => () => onReadStateChange?.(false), [onReadStateChange]);

  const accessLost = sessionCapabilityLost || requestCapabilityLost;
  const interactionBlocked = locked || readStateBlocked;
  const sourceQueryValid = validManagementSearch(sourceQueryDraft, 128);
  const keyQueryValid = validManagementSearch(keyQueryDraft, 128);
  const candidateQueryValid = validManagementSearch(candidateQueryDraft, 512);
  const resetKeySelection = () => {
    setSelectedKey(null);
    setKeyQueryDraft('');
    setKeyQuery('');
    setCandidateQueryDraft('');
    setCandidateQuery('');
    setCandidateSource('');
  };
  const chooseSource = (next: DonationSourceSummary | null) => {
    if (interactionBlocked) return;
    setSelectedSource(next);
    resetKeySelection();
  };
  const chooseKey = (next: ManagedDonationKeySummary | null) => {
    if (interactionBlocked || (next !== null && isTerminalKey(next))) return;
    setSelectedKey(next);
    setCandidateQueryDraft('');
    setCandidateQuery('');
    setCandidateSource('');
  };
  const changeScope = (next: SourceScope) => {
    if (interactionBlocked || next === scope) return;
    setScope(next);
    setSelectedSource(null);
    resetKeySelection();
  };
  const submitSourceQuery = () => {
    if (interactionBlocked || !sourceQueryValid) return;
    setSourceQuery(sourceQueryDraft.trim());
    setSelectedSource(null);
    resetKeySelection();
  };
  const submitKeyQuery = () => {
    if (interactionBlocked || !keyQueryValid) return;
    setKeyQuery(keyQueryDraft.trim());
  };
  const submitCandidateQuery = () => {
    if (interactionBlocked || !candidateQueryValid) return;
    setCandidateQuery(candidateQueryDraft.trim());
  };
  const clearSource = () => chooseSource(null);
  const clearKey = () => chooseKey(null);

  if (accessLost) return <p role="alert">{t('common.operations.charity.accessLost')}</p>;
  if (sessionError && !sessionErrorLost) return <ErrorState error={sessionError} />;
  if (!account || !management.authorityReady) return <LoadingState />;

  const sourcePage = sources.data;
  const keyPage = sourceKeys.data;
  const candidatePage = candidates.data;
  const sourceBusy = interactionBlocked || sourceReadBlocked;
  const keyBusy = interactionBlocked || sourceKeyReadBlocked;
  const candidateBusy = interactionBlocked || candidateReadBlocked;
  const allSelectedEntries = Object.entries(selected);
  const selectedEntries = allSelectedEntries.filter(([, entry]) =>
    entry.upstream_model_id.toLowerCase().includes(selectedSearch.toLowerCase()),
  );
  const selectedPageSize = selectedPager.pageSize;
  const selectedTotalPages = Math.max(1, Math.ceil(selectedEntries.length / selectedPageSize));
  const selectedPage = Math.min(Number(selectedPager.page), selectedTotalPages);
  const selectedPageMetadata: PageMetadata = {
    page: String(selectedPage),
    page_size: selectedPageSize,
    total_items: String(selectedEntries.length),
    total_pages: String(selectedTotalPages),
  };
  const selectedPageEntries = selectedEntries.slice(
    (selectedPage - 1) * selectedPageSize,
    selectedPage * selectedPageSize,
  );

  const picker = (
    <div
      className="ops-binding-picker charity-binding-picker"
      hidden={managingKey}
      ref={listRef}
      tabIndex={-1}
    >
      {!fixedKey ? (
        <nav className="ops-actions" aria-label={t('common.operations.charity.bindingCandidates')}>
          <Button
            type="button"
            variant="ghost"
            aria-current={!selectedSource ? 'step' : undefined}
            disabled={interactionBlocked}
            onClick={clearSource}
          >
            {t('common.operations.charity.chooseSource')}
          </Button>
          {selectedSource ? (
            <>
              <span aria-hidden="true">/</span>
              <Button
                type="button"
                variant="ghost"
                aria-current={!selectedKey ? 'step' : undefined}
                disabled={interactionBlocked}
                onClick={clearKey}
              >
                {t('common.operations.charity.chooseKey')}
              </Button>
            </>
          ) : null}
          {selectedKey ? (
            <>
              <span aria-hidden="true">/</span>
              <span aria-current="step">{t('common.operations.charity.chooseModels')}</span>
            </>
          ) : null}
        </nav>
      ) : null}

      {!selectedSource ? (
        <>
          <div className="ops-toolbar">
            <KeyStateFilter
              value={keyState}
              disabled={interactionBlocked}
              onChange={(state) => {
                setKeyState(state);
                setScope('all');
                setSelectedSource(null);
                resetKeySelection();
              }}
            />
            <form
              onSubmit={(event) => {
                event.preventDefault();
                submitSourceQuery();
              }}
            >
              <label>
                <span>{t('common.operations.charity.searchSources')}</span>
                <input
                  type="search"
                  value={sourceQueryDraft}
                  maxLength={256}
                  disabled={interactionBlocked}
                  aria-invalid={!sourceQueryValid}
                  onChange={(event) => setSourceQueryDraft(event.target.value)}
                />
                {!sourceQueryValid ? (
                  <span className="field-error" role="alert">
                    {t('common.operations.charity.searchInvalid')}
                  </span>
                ) : null}
              </label>
              <Button
                type="submit"

                disabled={interactionBlocked || !sourceQueryValid}
              >
                {t('common.search')}
              </Button>
            </form>
            <label>
              <span>{t('common.operations.charity.sourceScope')}</span>
              <select
                value={scope}
                disabled={interactionBlocked}
                onChange={(event) => changeScope(event.target.value as SourceScope)}
              >
                <option value="active">{t('common.operations.charity.scopeActive')}</option>
                <option value="all">{t('common.operations.charity.scopeAll')}</option>
              </select>
            </label>
          </div>
          {sources.isPending ? (
            <LoadingState />
          ) : sources.error ? (
            <ErrorState error={sources.error} onRetry={() => void sources.refetch()} />
          ) : sourcePage ? (
            <div aria-busy={sources.isFetching}>
              {sourcePage.data.length === 0 ? (
                <EmptyState
                  title={t('common.operations.charity.noSourceGroups')}
                  body={t('common.operations.charity.noSourceGroupsBody')}
                />
              ) : (
                <section
                  className="nb-choice-list"
                  aria-label={t('common.operations.charity.chooseSource')}
                >
                  <ul className="nb-choice-list__items">
                    {sourcePage.data.map((entry) => (
                      <li key={entry.source_key}>
                        <button
                          type="button"
                          className="ops-picker-choice"
                          disabled={interactionBlocked}
                          onClick={() => chooseSource(entry)}
                        >
                          <strong>{sourceTitle(entry.safe_source, t)}</strong>
                          <code>{sourceBaseURL(entry.safe_source)}</code>
                          {entry.safe_source.kind === 'mainstream' ? (
                            <span>{entry.safe_source.name}</span>
                          ) : null}
                          <small>
                            {t('common.operations.charity.sourceDonationCount', {
                              count: entry.donation_count,
                            })}{' '}
                            {t('common.operations.charity.sourceKeyCount', {
                              count: entry.key_count,
                            })}{' '}
                            {t('common.operations.charity.sourceUsableKeyCount', {
                              count: entry.usable_key_count,
                            })}{' '}
                            {t('common.operations.charity.sourcePendingDonationCount', {
                              count: entry.pending_donation_count,
                            })}
                          </small>
                        </button>
                      </li>
                    ))}
                  </ul>
                </section>
              )}
              <PagePagination
                metadata={sourcePage.pagination}
                requestedPage={sourcePager.page}
                busy={sourceBusy}
                onPageChange={sourcePager.setPage}
                onPageSizeChange={sourcePager.setPageSize}
              />
            </div>
          ) : (
            <LoadingState />
          )}
        </>
      ) : (
        <>
          {sources.error ? (
            <ErrorState error={sources.error} onRetry={() => void sources.refetch()} />
          ) : null}
          <div className="ops-picker-context">
            <strong>{sourceTitle(selectedSource.safe_source, t)}</strong>
            <code>{sourceBaseURL(selectedSource.safe_source)}</code>
            <span>
              {t('common.operations.charity.sourceDonationCount', {
                count: selectedSource.donation_count,
              })}{' '}
              {t('common.operations.charity.sourceKeyCount', {
                count: selectedSource.key_count,
              })}
            </span>
          </div>
          {!selectedKey ? (
            <>
              <div className="ops-toolbar">
                <form
                  onSubmit={(event) => {
                    event.preventDefault();
                    submitKeyQuery();
                  }}
                >
                  <label>
                    <span>{t('common.operations.charity.searchKeys')}</span>
                    <input
                      type="search"
                      value={keyQueryDraft}
                      maxLength={256}
                      disabled={interactionBlocked}
                      aria-invalid={!keyQueryValid}
                      onChange={(event) => setKeyQueryDraft(event.target.value)}
                    />
                    {!keyQueryValid ? (
                      <span className="field-error" role="alert">
                        {t('common.operations.charity.searchInvalid')}
                      </span>
                    ) : null}
                  </label>
                  <Button
                    type="submit"

                    disabled={interactionBlocked || !keyQueryValid}
                  >
                    {t('common.search')}
                  </Button>
                </form>
              </div>
              {sourceKeys.isPending ? (
                <LoadingState />
              ) : sourceKeys.error ? (
                <ErrorState error={sourceKeys.error} onRetry={() => void sourceKeys.refetch()} />
              ) : keyPage ? (
                <div aria-busy={sourceKeys.isFetching}>
                  {keyPage.data.length === 0 ? (
                    <EmptyState
                      title={t('common.operations.charity.noSourceKeys')}
                      body={t('common.operations.charity.noSourceKeysBody')}
                    />
                  ) : (
                    <section
                      className="nb-choice-list"
                      aria-label={t('common.operations.charity.chooseKey')}
                    >
                      <ul className="nb-choice-list__items">
                        {keyPage.data.map((entry) => {
                          const terminal = isTerminalKey(entry);
                          return (
                            <li key={entry.key_id}>
                              <button
                                type="button"
                                className="ops-picker-choice"
                                disabled={interactionBlocked || terminal}
                                onClick={() => chooseKey(entry)}
                              >
                                <strong>
                                  {entry.safe_note || `${entry.display_head}…${entry.display_tail}`}
                                </strong>
                                <DonationKeyNotes
                                  donor={entry.donation_note}
                                  approval={entry.approval_note}
                                />
                                <span>
                                  {t('common.operations.charity.donationNumber', {
                                    id: entry.donation_id,
                                  })}{' '}
                                  ·{' '}
                                  {t('common.operations.charity.charityStateLabel', {
                                    state: t(
                                      `common.operations.charity.charityState.${entry.charity_state}`,
                                    ),
                                  })}
                                </span>
                                <code>
                                  {entry.display_head}…{entry.display_tail}
                                </code>
                                <span>{entry.safe_source.base_url}</span>
                                <KeyLimitSummary
                                  concurrency={entry.max_concurrency}
                                  rpm={entry.max_rpm}
                                  readOnly
                                />
                                {entry.rate_limited_until !== null || entry.smart_backoff ? (
                                  <KeyRateLimitTime
                                    until={entry.rate_limited_until}
                                    smartBackoff={entry.smart_backoff}
                                  />
                                ) : null}
                                <small>
                                  {entry.idle
                                    ? t('common.donationHandling.idle')
                                    : t('common.donationHandling.bound', {
                                        count: entry.binding_count,
                                      })}
                                  {' · '}
                                  {t('common.donationHandling.title')}:{' '}
                                  {t(`common.donationHandling.states.${entry.handling.state}`)}
                                </small>
                                {entry.ended_reason ? (
                                  <small>
                                    {t('common.operations.charity.endedReason')}:{' '}
                                    {t(`common.donationHandling.reasons.${entry.ended_reason}`)}
                                  </small>
                                ) : null}
                                {terminal ? (
                                  <small>{t('common.operations.charity.keyUnavailable')}</small>
                                ) : null}
                              </button>
                            </li>
                          );
                        })}
                      </ul>
                    </section>
                  )}
                  <PagePagination
                    metadata={keyPage.pagination}
                    requestedPage={sourceKeyPager.page}
                    busy={keyBusy}
                    onPageChange={sourceKeyPager.setPage}
                    onPageSizeChange={sourceKeyPager.setPageSize}
                  />
                </div>
              ) : (
                <LoadingState />
              )}
            </>
          ) : (
            <>
              <p className="ops-picker-context">
                {selectedKey.safe_note || `${selectedKey.display_head}…${selectedKey.display_tail}`}{' '}
                ·{' '}
                {t('common.operations.charity.donationNumber', {
                  id: selectedKey.donation_id,
                })}{' '}
                · {selectedKey.safe_source.base_url}
              </p>
              <div className="charity-binding-key-shortcuts">
                {!managingKey ? (
                  <DonationDiscoveryControl
                    key={selectedKey.key_id}
                    role={role}
                    target={{ donation_id: selectedKey.donation_id, key_id: selectedKey.key_id }}
                    buttonLabel={t('common.operations.charity.refetchModels')}
                    disabled={
                      locked ||
                      !canRead ||
                      !(selectedKey.can_manage ?? selectedKey.charity_state === 'available')
                    }
                    onCapabilityLoss={onCapabilityLoss}
                  />
                ) : null}
                {renderKeyManager ? (
                  <Button
                    type="button"
                    disabled={locked || !canRead}
                    onClick={() => {
                      remember();
                      setManagingKey(true);
                    }}
                  >
                    {t('common.operations.charity.manageSelectedKey')}
                  </Button>
                ) : null}
              </div>
              {sourceKeys.error ? (
                <ErrorState error={sourceKeys.error} onRetry={() => void sourceKeys.refetch()} />
              ) : null}
              <div className="ops-toolbar">
                <form
                  onSubmit={(event) => {
                    event.preventDefault();
                    submitCandidateQuery();
                  }}
                >
                  <label>
                    <span>{t('common.operations.charity.searchModels')}</span>
                    <input
                      type="search"
                      value={candidateQueryDraft}
                      placeholder={t('common.workflow.upstreamSearch')}
                      maxLength={1024}
                      disabled={interactionBlocked}
                      aria-invalid={!candidateQueryValid}
                      onChange={(event) => setCandidateQueryDraft(event.target.value)}
                    />
                    {!candidateQueryValid ? (
                      <span className="field-error" role="alert">
                        {t('common.operations.charity.candidateSearchInvalid')}
                      </span>
                    ) : null}
                  </label>
                  <Button
                    type="submit"

                    disabled={interactionBlocked || !candidateQueryValid}
                  >
                    {t('common.search')}
                  </Button>
                </form>
                <label>
                  <span>{t('common.operations.charity.candidateSourceFilter')}</span>
                  <select
                    value={candidateSource}
                    disabled={interactionBlocked}
                    onChange={(event) => {
                      if (interactionBlocked) return;
                      setCandidateSource(event.target.value as CandidateSource);
                    }}
                  >
                    <option value="">{t('common.operations.charity.candidateSourceAny')}</option>
                    <option value="automatic">{t(sourceTypeKey('automatic'))}</option>
                    <option value="manual">{t(sourceTypeKey('manual'))}</option>
                  </select>
                </label>
              </div>
              {candidates.isPending ? (
                <LoadingState />
              ) : candidates.error ? (
                <ErrorState error={candidates.error} onRetry={() => void candidates.refetch()} />
              ) : candidatePage ? (
                <div aria-busy={candidates.isFetching}>
                  {candidatePage.data.length === 0 ? (
                    <EmptyState
                      title={t('common.operations.charity.noCandidates')}
                      body={t('common.operations.charity.noCandidatesBody')}
                    />
                  ) : (
                    <section
                      className="nb-choice-list"
                      aria-label={t('common.operations.charity.chooseModels')}
                    >
                      <ul className="nb-choice-list__items">
                        {candidatePage.data.map((entry) => {
                          const key = charitySelectionKey(entry);
                          return (
                            <li key={key}>
                              <label className="ops-picker-choice checkbox-label">
                                <input
                                  type="checkbox"
                                  disabled={
                                    interactionBlocked ||
                                    Boolean(entry.current_binding_id) ||
                                    entry.capabilities?.can_bind === false ||
                                    (!selected[key] && Object.keys(selected).length >= capacity)
                                  }
                                  checked={Boolean(selected[key])}
                                  onChange={(event) => {
                                    if (
                                      interactionBlocked ||
                                      Boolean(entry.current_binding_id) ||
                                      entry.capabilities?.can_bind === false ||
                                      (event.target.checked &&
                                        !selected[key] &&
                                        Object.keys(selected).length >= capacity)
                                    )
                                      return;
                                    const next = { ...selected };
                                    if (event.target.checked) {
                                      next[key] = {
                                        ...entry,
                                        note: selectedKey.safe_note,
                                        selected_native_services: entry.capabilities?.can_bind
                                          ? entry.native_services?.slice()
                                          : undefined,
                                      };
                                    } else {
                                      delete next[key];
                                    }
                                    onChange(next);
                                  }}
                                />
                                <span>
                                  {entry.upstream_model_id}
                                  {entry.current_binding_id ? (
                                    <small className="ops-picker-source-types">
                                      {t('common.bindingBatch.boundHere')}
                                    </small>
                                  ) : null}
                                  {entry.other_bindings?.length ? (
                                    <small className="ops-picker-source-types">
                                      {t('common.bindingBatch.boundElsewhere', {
                                        names: entry.other_bindings
                                          .map((binding) => binding.full_name)
                                          .join(' / '),
                                      })}
                                    </small>
                                  ) : null}
                                  {entry.other_binding_count &&
                                  BigInt(entry.other_binding_count) >
                                    BigInt(entry.other_bindings?.length ?? 0) ? (
                                    <small className="ops-picker-source-types">
                                      {t('common.bindingBatch.moreBindings', {
                                        count: Number(
                                          BigInt(entry.other_binding_count) -
                                            BigInt(entry.other_bindings?.length ?? 0),
                                        ),
                                      })}
                                    </small>
                                  ) : null}
                                  <small className="ops-picker-source-types">
                                    {entry.source_types
                                      .map((source) => t(sourceTypeKey(source)))
                                      .join(' / ')}
                                  </small>
                                </span>
                              </label>
                              {entry.other_binding_count &&
                              BigInt(entry.other_binding_count) >
                                BigInt(entry.other_bindings?.length ?? 0) &&
                              (onInspectBindings || renderKeyManager) ? (
                                <Button
                                  type="button"
                                  disabled={interactionBlocked}
                                  onClick={() => {
                                    if (onInspectBindings) onInspectBindings();
                                    else {
                                      remember();
                                      setManagingKey(true);
                                    }
                                  }}
                                >
                                  {t('common.keyModels.title')}
                                </Button>
                              ) : null}
                            </li>
                          );
                        })}
                      </ul>
                    </section>
                  )}
                  <PagePagination
                    metadata={candidatePage.pagination}
                    requestedPage={candidatePager.page}
                    busy={candidateBusy}
                    onPageChange={candidatePager.setPage}
                    onPageSizeChange={candidatePager.setPageSize}
                  />
                  <div className="nb-actions">
                    <Button
                      type="button"
                      disabled={
                        candidateBusy ||
                        candidatePage.data.every((entry) => Boolean(entry.current_binding_id)) ||
                        candidatePage.data.filter(
                          (entry) =>
                            !entry.current_binding_id && !selected[charitySelectionKey(entry)],
                        ).length >
                          capacity - allSelectedEntries.length
                      }
                      onClick={() => {
                        const next = { ...selected };
                        for (const entry of candidatePage.data.filter(
                          (entry) =>
                            !entry.current_binding_id && entry.capabilities?.can_bind !== false,
                        ))
                          next[charitySelectionKey(entry)] ??= {
                            ...entry,
                            note: selectedKey.safe_note,
                            selected_native_services: entry.capabilities?.can_bind
                              ? entry.native_services?.slice()
                              : undefined,
                          };
                        onChange(next);
                      }}
                    >
                      {t('common.workflow.selectPage', {
                        count: candidatePage.data.filter((entry) => !entry.current_binding_id)
                          .length,
                      })}
                    </Button>
                    <Button
                      type="button"
                      disabled={candidateBusy}
                      onClick={() => {
                        const next = { ...selected };
                        for (const entry of candidatePage.data)
                          delete next[charitySelectionKey(entry)];
                        onChange(next);
                      }}
                    >
                      {t('common.workflow.clearPage')}
                    </Button>
                  </div>
                </div>
              ) : (
                <LoadingState />
              )}
            </>
          )}
        </>
      )}

      {showSelectionSummary ? (
        <section
          className="ops-picker-selection"
          aria-label={t('common.operations.charity.selectedModels')}
        >
          <h4>
            {t('common.operations.charity.selectedCount', { count: Object.keys(selected).length })}
          </h4>
          <p>{t('common.operations.charity.crossSelectionHelp')}</p>
          <p role="status" aria-live="polite">
            {t('common.workflow.selectionCapacity', {
              selected: allSelectedEntries.length,
              capacity,
              outside: allSelectedEntries.filter(
                ([key]) => !candidatePage?.data.some((entry) => charitySelectionKey(entry) === key),
              ).length,
            })}
          </p>
          <Field label={t('common.workflow.searchSelected')}>
            {(props) => (
              <input
                {...props}
                type="search"
                placeholder={t('common.workflow.upstreamSearch')}
                value={selectedSearch}
                ref={selectedSearchInput}
                onChange={(event) => setSelectedSearch(event.target.value)}
              />
            )}
          </Field>
          <Button
            type="button"
            disabled={locked || allSelectedEntries.length === 0}
            onClick={() => {
              onChange({});
              selectedSearchInput.current?.focus();
            }}
          >
            {t('common.workflow.clearSelected')}
          </Button>
          <ul>
            {selectedPageEntries.map(([key, entry]) => (
              <li key={key}>
                <div>
                  <strong>
                    {allSelectedEntries.findIndex(([id]) => id === key) + 1}.{' '}
                    {entry.upstream_model_id}
                  </strong>
                  <span>
                    {t('common.operations.charity.donationNumber', { id: entry.donation_id })} ·{' '}
                    {entry.note} · {entry.source.display_head}…{entry.source.display_tail}
                  </span>
                </div>
                {entry.native_services ? (
                  <NativeServicesField
                    options={entry.native_services}
                    value={entry.selected_native_services ?? []}
                    onChange={(value) =>
                      onChange({
                        ...selected,
                        [key]: { ...entry, selected_native_services: value },
                      })
                    }
                    disabled={interactionBlocked}
                  />
                ) : null}
                <Button
                  type="button"
                  variant="ghost"
                  aria-label={t('common.workflow.removeSelection', { id: entry.upstream_model_id })}
                  disabled={interactionBlocked}
                  onClick={() => {
                    const next = { ...selected };
                    delete next[key];
                    onChange(next);
                    selectedSearchInput.current?.focus();
                  }}
                >
                  {t('common.operations.charity.remove')}
                </Button>
              </li>
            ))}
          </ul>
          <PagePagination
            metadata={selectedPageMetadata}
            requestedPage={selectedPager.page}
            busy={interactionBlocked}
            onPageChange={selectedPager.setPage}
            onPageSizeChange={selectedPager.setPageSize}
          />
        </section>
      ) : null}
    </div>
  );
  return (
    <>
      {managingKey && selectedKey && renderKeyManager ? (
        <div ref={detailRef} tabIndex={-1}>
          {renderKeyManager({
            modelID: modelId,
            donationID: selectedKey.donation_id,
            keyID: selectedKey.key_id,
            onBack: () => setManagingKey(false),
          })}
        </div>
      ) : null}
      {picker}
    </>
  );
}
