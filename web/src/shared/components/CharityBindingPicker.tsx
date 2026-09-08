import { useEffect, useRef, useState } from 'react';
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

export type CharitySelection = CharityBindingCandidate & { note: string };

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
  return typeof id === 'string' && id.length > 0 && level === 5 ? id : undefined;
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
    if (isUnauthorized(error) || isForbidden(error)) clearStationSession(client, frame);
    throw error;
  }
}

function sourceBaseURL(source: DonationPageSafeSource): string {
  return source.base_url;
}

function isTerminalKey(key: ManagedDonationKeySummary): boolean {
  return key.charity_state === 'ended' || key.charity_state === 'expired';
}

function sourcePageFilters(query: string, scope: SourceScope) {
  return { q: query || undefined, scope } as const;
}

function sourceKeyPageFilters(query: string, scope: SourceScope) {
  return { q: query || undefined, scope } as const;
}

function candidatePageFilters(
  key: ManagedDonationKeySummary,
  query: string,
  source: CandidateSource,
): CharityBindingCandidatePageFilters {
  return {
    donation_id: key.donation_id,
    donation_key_id: key.key_id,
    q: query || undefined,
    source: source || undefined,
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

interface CharityBindingPickerProps {
  role: CharityRole;
  modelId: string;
  selected: Record<string, CharitySelection>;
  onChange: (next: Record<string, CharitySelection>) => void;
  locked: boolean;
  onCapabilityLoss?: () => void;
  onReadStateChange?: (blocked: boolean) => void;
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
    queryFn: async () => null,
    enabled: false,
    retry: false,
  });
  const account = sessionAccount(props.role, session.data);
  const identity = `${props.role}:${props.modelId}:${account ?? 'none'}`;
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
  role,
  modelId,
  selected,
  onChange,
  locked,
  onCapabilityLoss,
  onReadStateChange,
  account,
  management,
  sessionError,
}: CharityBindingPickerProps & {
  account: string | undefined;
  management: ManagementCapability;
  sessionError: unknown;
}) {
  const { t } = useTranslation();
  const client = useQueryClient();
  const [scope, setScope] = useState<SourceScope>('active');
  const [sourceQueryDraft, setSourceQueryDraft] = useState('');
  const [sourceQuery, setSourceQuery] = useState('');
  const [selectedSource, setSelectedSource] = useState<DonationSourceSummary | null>(null);
  const [keyQueryDraft, setKeyQueryDraft] = useState('');
  const [keyQuery, setKeyQuery] = useState('');
  const [selectedKey, setSelectedKey] = useState<ManagedDonationKeySummary | null>(null);
  const [candidateQueryDraft, setCandidateQueryDraft] = useState('');
  const [candidateQuery, setCandidateQuery] = useState('');
  const [candidateSource, setCandidateSource] = useState<CandidateSource>('');
  const lossNotified = useRef(false);

  const station = role === 'admin' ? 'admin' : 'user';
  const sourcePager = usePagePager({
    station,
    listType: sourcePageList,
    scopeKey: account ?? 'no-account',
    resetKey: `${scope}:${sourceQuery}`,
  });
  const sourceKeyPager = usePagePager({
    station,
    listType: sourceKeyPageList,
    scopeKey: `${account ?? 'no-account'}:${selectedSource?.source_key ?? 'no-source'}`,
    resetKey: `${scope}:${keyQuery}`,
  });
  const candidatePager = usePagePager({
    station,
    listType: candidatePageList,
    scopeKey: `${account ?? 'no-account'}:${modelId}:${selectedKey?.key_id ?? 'no-key'}`,
    resetKey: `${candidateQuery}:${candidateSource}`,
  });
  const selectedPager = usePagePager({
    station,
    listType: 'charity-binding-selected',
    scopeKey: `${account ?? 'no-account'}:${modelId}`,
  });

  const sessionCapabilityLost = management.data === true;
  const sessionErrorLost = isUnauthorized(sessionError) || isForbidden(sessionError);
  const canRead =
    Boolean(account) && management.authorityReady && !sessionCapabilityLost && !sessionError;
  const sourceQueryIdentity = [
    ...charityKeys.root(role),
    'binding-picker',
    account ?? 'no-account',
    modelId,
    'sources',
    scope,
    sourceQuery,
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
    selectedKey?.key_id ?? 'no-key',
    candidateQuery,
    candidateSource,
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
          sourcePageFilters(sourceQuery, scope),
          sourcePager.page,
          sourcePager.pageSize,
          signal,
        ),
      ),
    enabled: canRead,
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
          sourceKeyPageFilters(keyQuery, scope),
          sourceKeyPager.page,
          sourceKeyPager.pageSize,
          signal,
        ),
      ),
    enabled: canRead && Boolean(selectedSource),
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
  const candidates = useQuery({
    queryKey: candidateQueryKey,
    queryFn: ({ signal }) =>
      guardedRead(client, role, account, () =>
        getManagedBindingCandidatesPage(
          role,
          modelId,
          candidatePageFilters(selectedKey!, candidateQuery, candidateSource),
          candidatePager.page,
          candidatePager.pageSize,
          signal,
        ),
      ),
    enabled: canRead && Boolean(selectedSource && selectedKey),
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

  const sourceReadActive = canRead;
  const sourceKeyReadActive = canRead && Boolean(selectedSource);
  const candidateReadActive = canRead && Boolean(selectedSource && selectedKey);
  const sourceReadBlocked =
    sourceReadActive && (sources.isPending || sources.isFetching || Boolean(sources.error));
  const sourceKeyReadBlocked =
    sourceKeyReadActive &&
    (sourceKeys.isPending || sourceKeys.isFetching || Boolean(sourceKeys.error));
  const candidateReadBlocked =
    candidateReadActive &&
    (candidates.isPending || candidates.isFetching || Boolean(candidates.error));
  const readStateBlocked =
    !canRead ||
    requestCapabilityLost ||
    sourceReadBlocked ||
    sourceKeyReadBlocked ||
    candidateReadBlocked;
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
  const sourceBusy = interactionBlocked;
  const keyBusy = interactionBlocked;
  const candidateBusy = interactionBlocked;
  const selectedEntries = Object.entries(selected);
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

  return (
    <div className="ops-binding-picker">
      <nav className="ops-actions" aria-label={t('common.operations.charity.bindingCandidates')}>
        <button
          type="button"
          className="btn btn-quiet"
          aria-current={!selectedSource ? 'step' : undefined}
          disabled={interactionBlocked}
          onClick={clearSource}
        >
          {t('common.operations.charity.chooseSource')}
        </button>
        {selectedSource ? (
          <>
            <span aria-hidden="true">/</span>
            <button
              type="button"
              className="btn btn-quiet"
              aria-current={!selectedKey ? 'step' : undefined}
              disabled={interactionBlocked}
              onClick={clearKey}
            >
              {t('common.operations.charity.chooseKey')}
            </button>
          </>
        ) : null}
        {selectedKey ? (
          <>
            <span aria-hidden="true">/</span>
            <span aria-current="step">{t('common.operations.charity.chooseModels')}</span>
          </>
        ) : null}
      </nav>

      {!selectedSource ? (
        <>
          <div className="ops-toolbar">
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
              <button
                type="submit"
                className="btn btn-secondary"
                disabled={interactionBlocked || !sourceQueryValid}
              >
                {t('common.search')}
              </button>
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
                  <button
                    type="submit"
                    className="btn btn-secondary"
                    disabled={interactionBlocked || !keyQueryValid}
                  >
                    {t('common.search')}
                  </button>
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
                  <button
                    type="submit"
                    className="btn btn-secondary"
                    disabled={interactionBlocked || !candidateQueryValid}
                  >
                    {t('common.search')}
                  </button>
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
                                    (!selected[key] && Object.keys(selected).length >= 100)
                                  }
                                  checked={Boolean(selected[key])}
                                  onChange={(event) => {
                                    if (
                                      interactionBlocked ||
                                      (event.target.checked &&
                                        !selected[key] &&
                                        Object.keys(selected).length >= 100)
                                    )
                                      return;
                                    const next = { ...selected };
                                    if (event.target.checked) {
                                      next[key] = { ...entry, note: selectedKey.safe_note };
                                    } else {
                                      delete next[key];
                                    }
                                    onChange(next);
                                  }}
                                />
                                <span>
                                  {entry.upstream_model_id}
                                  <small className="ops-picker-source-types">
                                    {entry.source_types
                                      .map((source) => t(sourceTypeKey(source)))
                                      .join(' / ')}
                                  </small>
                                </span>
                              </label>
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
                </div>
              ) : (
                <LoadingState />
              )}
            </>
          )}
        </>
      )}

      <section
        className="ops-picker-selection"
        aria-label={t('common.operations.charity.selectedModels')}
      >
        <h4>
          {t('common.operations.charity.selectedCount', { count: Object.keys(selected).length })}
        </h4>
        <p>{t('common.operations.charity.crossSelectionHelp')}</p>
        <ul>
          {selectedPageEntries.map(([key, entry]) => (
            <li key={key}>
              <div>
                <strong>{entry.upstream_model_id}</strong>
                <span>
                  {t('common.operations.charity.donationNumber', { id: entry.donation_id })} ·{' '}
                  {entry.note} · {entry.source.display_head}…{entry.source.display_tail}
                </span>
              </div>
              <button
                type="button"
                className="btn btn-quiet"
                disabled={interactionBlocked}
                onClick={() => {
                  const next = { ...selected };
                  delete next[key];
                  onChange(next);
                }}
              >
                {t('common.operations.charity.remove')}
              </button>
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
    </div>
  );
}
