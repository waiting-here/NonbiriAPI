import {
  BindingSourceChoice,
  BindingKeyChoice,
  BindingModelChoice,
} from './charity/BindingChoices';
import { BindingRouteGuide, BindingServiceEditor } from './charity/BindingRouteGuide';
import { Fold } from './ui';
import type { NativeService } from '@shared/nativeServices';
import { smartBackoffDeadline } from '@shared/operations/smartBackoff';
import { Button } from '@shared/components/ui/Button';
import { Field } from './ui/Field';
import { FilterBar } from './ui/FilterBar';
import { KeyStateFilter } from './charity/KeyStateFilter';
import type { KeyStateFilter as KeyState } from '@shared/operations/keyState';
import { useEffect, useRef, useState, type ReactNode } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import type { TFunction } from 'i18next';
import { useManagementCapability } from '@shared/charityManagement';
import { useBindingPage, sessionAccount } from './charity/useBindingPage';
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
  suggestedBindingServices,
  type CharityBindingCandidate,
  type CharityRole,
} from '@shared/operations/charity';
import type { PageMetadata } from '@shared/operations/pageNumbers';
import { EmptyState, ErrorState, LoadingState } from './States';
import { useRateLimitRefresh } from '@shared/operations/useRateLimitRefresh';
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

function sourceTitle(source: DonationPageSafeSource, translate: TFunction): string {
  return source.kind === 'mainstream'
    ? translate('common.operations.charity.mainstreamSource', { name: source.name })
    : translate('common.operations.charity.customSource');
}

function isTerminalKey(key: ManagedDonationKeySummary): boolean {
  return key.charity_state === 'ended' || key.charity_state === 'expired';
}

function sourcePageFilters(query: string, scope: SourceScope, key_state: KeyState) {
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

export interface CharityPickerKeyManagerProps {
  disabled?: boolean;
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
  const [keyState, setKeyState] = useState<KeyState>('all');
  const [selectedSearch, setSelectedSearch] = useState('');
  const selectedSearchInput = useRef<HTMLInputElement>(null);
  const browserRef = useRef<HTMLDivElement>(null);
  const selectionHeading = useRef<HTMLHeadingElement>(null);
  const pendingFocus = useRef<{ restoreId?: string } | null>(null);
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
  const bindingScope = { role, account, modelId };
  const stewardModel = role === 'steward' ? modelId : undefined;
  const sourceID = selectedSource?.source_key ?? 'no-source';
  const sources = useBindingPage(
    bindingScope,
    ['sources', scope, sourceQuery, keyState],
    sourcePager,
    readEnabled && !fixedKey,
    (signal) =>
      getDonationSourcesPage(
        role,
        sourcePageFilters(sourceQuery, scope, keyState),
        sourcePager.page,
        sourcePager.pageSize,
        signal,
        stewardModel,
      ),
  );
  const sourceKeys = useBindingPage(
    bindingScope,
    ['source-keys', sourceID, scope, keyQuery, keyState],
    sourceKeyPager,
    readEnabled && Boolean(selectedSource) && !fixedKey,
    (signal) =>
      getDonationSourceKeysPage(
        role,
        selectedSource?.source_key ?? '',
        sourcePageFilters(keyQuery, scope, keyState),
        sourceKeyPager.page,
        sourceKeyPager.pageSize,
        signal,
        stewardModel,
      ),
  );
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
  const candidates = useBindingPage(
    bindingScope,
    [
      'candidates',
      sourceID,
      selectedKeySnapshot?.key_id ?? 'no-key',
      candidateQuery,
      candidateSource,
      keyState,
    ],
    candidatePager,
    readEnabled && Boolean(selectedSource && selectedKey),
    (signal) =>
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
  );

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
    pendingFocus.current = next ? {} : { restoreId: selectedSource?.source_key };
    setSelectedSource(next);
    resetKeySelection();
  };
  const chooseKey = (next: ManagedDonationKeySummary | null) => {
    if (interactionBlocked || (next !== null && isTerminalKey(next))) return;
    pendingFocus.current = next ? {} : { restoreId: selectedKey?.key_id };
    setSelectedKey(next);
    setCandidateQueryDraft('');
    setCandidateQuery('');
    setCandidateSource('');
  };
  useEffect(() => {
    const requested = pendingFocus.current;
    const browser = browserRef.current;
    if (!requested || !browser) return;
    const active = document.activeElement;
    if (active && active !== document.body && !browser.contains(active)) {
      pendingFocus.current = null;
      return;
    }
    let target: HTMLElement | null = null;
    if (requested.restoreId) {
      const choices = browser.querySelectorAll<HTMLButtonElement>(
        'button[data-binding-choice]:enabled',
      );
      target =
        Array.from(choices).find((item) => item.dataset.bindingChoice === requested.restoreId) ??
        null;
      if (!target && (selectedSource ? sourceKeys.isPending : sources.isPending)) return;
    }
    target ??= browser.querySelector<HTMLInputElement>('input[type="search"]:enabled');
    pendingFocus.current = null;
    target?.focus();
  }, [selectedSource, selectedKeySnapshot, sourceKeys.isPending, sources.isPending]);
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
  const keyGuide = candidatePage?.data[0]?.binding_guide;
  const selectable =
    candidatePage?.data.filter(
      (entry) => !entry.current_binding_id && entry.capabilities.can_bind,
    ) ?? [];
  const selectCandidate = (entry: CharityBindingCandidate): CharitySelection => ({
    ...entry,
    note: selectedKey?.safe_note ?? '',
    selected_native_services: suggestedBindingServices(entry),
  });
  const sourceBusy = interactionBlocked || sourceReadBlocked;
  const keyBusy = interactionBlocked || sourceKeyReadBlocked;
  const candidateBusy = interactionBlocked || candidateReadBlocked;
  const allSelectedEntries = Object.entries(selected);
  const remainingOnPage = selectable.filter(
    (entry) => !selected[charitySelectionKey(entry)],
  ).length;
  const selectedOnPage = candidatePage?.data.some((entry) =>
    Boolean(selected[charitySelectionKey(entry)]),
  );
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
      <div
        className="binding-workspace"
        data-selection-rail={
          showSelectionSummary && (Boolean(selectedKey) || allSelectedEntries.length > 0)
        }
      >
        <div className="binding-browser" ref={browserRef}>
          {!fixedKey && (
            <nav
              className="binding-steps"
              aria-label={t('common.operations.charity.bindingCandidates')}
            >
              {!selectedSource ? (
                <strong aria-current="step">{t('common.operations.charity.chooseSource')}</strong>
              ) : (
                <Button
                  type="button"
                  variant="ghost"
                  disabled={interactionBlocked}
                  onClick={clearSource}
                >
                  {t('common.operations.charity.chooseSource')}
                </Button>
              )}
              <span aria-hidden="true">›</span>
              {selectedKey ? (
                <Button
                  type="button"
                  variant="ghost"
                  disabled={interactionBlocked}
                  onClick={clearKey}
                >
                  {t('common.operations.charity.chooseKey')}
                </Button>
              ) : (
                <span aria-current={selectedSource ? 'step' : undefined}>
                  {t('common.operations.charity.chooseKey')}
                </span>
              )}
              <span aria-hidden="true">›</span>
              <span aria-current={selectedKey ? 'step' : undefined}>
                {t('common.operations.charity.chooseModels')}
              </span>
            </nav>
          )}
          {!selectedSource ? (
            <>
              <FilterBar
                query={sources}
                activeCount={
                  Number(Boolean(sourceQuery)) +
                  Number(scope !== 'all') +
                  Number(keyState !== 'all')
                }
                submitDisabled={interactionBlocked || !sourceQueryValid}
                resetDisabled={interactionBlocked}
                onSubmit={submitSourceQuery}
                onReset={() => {
                  if (interactionBlocked) return;
                  setSourceQueryDraft('');
                  setSourceQuery('');
                  setKeyState('all');
                  changeScope('all');
                }}

                search={
                  <>
                    <Field
                      label={t('common.operations.charity.searchSources')}
                      error={
                        !sourceQueryValid ? t('common.operations.charity.searchInvalid') : undefined
                      }
                    >
                      {(field) => (
                        <input
                          {...field}
                          placeholder={t('common.operations.charity.searchSources')}
                          type="search"
                          value={sourceQueryDraft}
                          maxLength={256}
                          disabled={interactionBlocked}
                          aria-invalid={!sourceQueryValid}
                          onChange={(event) => setSourceQueryDraft(event.target.value)}
                        />
                      )}
                    </Field>
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
                    <Field label={t('common.operations.charity.sourceScope')} size="short">
                      {(field) => (
                        <select
                          {...field}
                          value={scope}
                          disabled={interactionBlocked}
                          onChange={(event) => changeScope(event.target.value as SourceScope)}
                        >
                          <option value="active">
                            {t('common.operations.charity.scopeActive')}
                          </option>
                          <option value="all">{t('common.operations.charity.scopeAll')}</option>
                        </select>
                      )}
                    </Field>
                  </>
                }
              />
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
                            <BindingSourceChoice
                              source={entry}
                              disabled={interactionBlocked || sources.isPlaceholderData}
                              onChoose={() => chooseSource(entry)}
                            />
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
              <div className="binding-current-source">
                <span className="binding-choice-identity">
                  <strong>{sourceTitle(selectedSource.safe_source, t)}</strong>
                  <code>{selectedSource.safe_source.base_url}</code>
                </span>
                {selectedKey && (
                  <span className="binding-current-key">
                    <strong>
                      {selectedKey.safe_note ||
                        `${selectedKey.display_head}…${selectedKey.display_tail}`}
                    </strong>
                    <span>
                      {t('common.operations.charity.donationNumber', {
                        id: selectedKey.donation_id,
                      })}
                    </span>
                  </span>
                )}
              </div>
              {!selectedKey ? (
                <>
                  <FilterBar
                    query={sourceKeys}
                    activeCount={keyQuery ? 1 : 0}
                    submitDisabled={interactionBlocked || !keyQueryValid}
                    resetDisabled={interactionBlocked}
                    onSubmit={submitKeyQuery}
                    onReset={() => {
                      if (interactionBlocked) return;
                      setKeyQueryDraft('');
                      setKeyQuery('');
                    }}
                    search={
                      <Field
                        label={t('common.operations.charity.searchKeys')}
                        error={
                          !keyQueryValid ? t('common.operations.charity.searchInvalid') : undefined
                        }
                      >
                        {(field) => (
                          <input
                            {...field}
                            placeholder={t('common.operations.charity.searchKeys')}
                            type="search"
                            value={keyQueryDraft}
                            maxLength={256}
                            disabled={interactionBlocked}
                            aria-invalid={!keyQueryValid}
                            onChange={(event) => setKeyQueryDraft(event.target.value)}
                          />
                        )}
                      </Field>
                    }
                  />
                  {sourceKeys.isPending ? (
                    <LoadingState />
                  ) : sourceKeys.error ? (
                    <ErrorState
                      error={sourceKeys.error}
                      onRetry={() => void sourceKeys.refetch()}
                    />
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
                                  <BindingKeyChoice
                                    item={entry}
                                    terminal={terminal}
                                    disabled={interactionBlocked || sourceKeys.isPlaceholderData}
                                    onChoose={() => chooseKey(entry)}
                                  />
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
                  <div className="charity-binding-key-shortcuts">
                    {!managingKey ? (
                      <DonationDiscoveryControl
                        key={selectedKey.key_id}
                        role={role}
                        target={{
                          donation_id: selectedKey.donation_id,
                          key_id: selectedKey.key_id,
                        }}
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
                    <ErrorState
                      error={sourceKeys.error}
                      onRetry={() => void sourceKeys.refetch()}
                    />
                  ) : null}
                  <FilterBar
                    query={candidates}
                    activeCount={[candidateQuery, candidateSource].filter(Boolean).length}
                    submitDisabled={interactionBlocked || !candidateQueryValid}
                    resetDisabled={interactionBlocked}
                    onSubmit={submitCandidateQuery}
                    onReset={() => {
                      if (interactionBlocked) return;
                      setCandidateQueryDraft('');
                      setCandidateQuery('');
                      setCandidateSource('');
                    }}
                    search={
                      <>
                        <Field
                          label={t('common.operations.charity.searchModels')}
                          error={
                            !candidateQueryValid
                              ? t('common.operations.charity.candidateSearchInvalid')
                              : undefined
                          }
                        >
                          {(field) => (
                            <input
                              {...field}
                              type="search"
                              value={candidateQueryDraft}
                              placeholder={t('common.workflow.upstreamSearch')}
                              maxLength={1024}
                              disabled={interactionBlocked}
                              aria-invalid={!candidateQueryValid}
                              onChange={(event) => setCandidateQueryDraft(event.target.value)}
                            />
                          )}
                        </Field>
                        <Field label={t('common.operations.charity.candidateSourceFilter')}>
                          {(field) => (
                            <select
                              {...field}
                              value={candidateSource}
                              disabled={interactionBlocked}
                              onChange={(event) => {
                                if (interactionBlocked) return;
                                setCandidateSource(event.target.value as CandidateSource);
                              }}
                            >
                              <option value="">
                                {t('common.operations.charity.candidateSourceAny')}
                              </option>
                              <option value="automatic">
                                {t('common.operations.charity.sourceType.automatic')}
                              </option>
                              <option value="manual">
                                {t('common.operations.charity.sourceType.manual')}
                              </option>
                            </select>
                          )}
                        </Field>
                      </>
                    }
                  />
                  {candidates.isPending ? (
                    <LoadingState />
                  ) : candidates.error ? (
                    <ErrorState
                      error={candidates.error}
                      onRetry={() => void candidates.refetch()}
                    />
                  ) : candidatePage ? (
                    <div aria-busy={candidates.isFetching}>
                      {keyGuide &&
                        (keyGuide.routes.length ? (
                          <Fold plain lazy title={t('common.bindingGuide.preview')}>
                            <BindingRouteGuide
                              guide={keyGuide}
                              selected={keyGuide.services.map((item) => item.native_service)}
                            />
                          </Fold>
                        ) : (
                          <p>{t('common.bindingGuide.noMatch')}</p>
                        ))}
                      {candidatePage.data.length > 0 && (
                        <div className="binding-model-toolbar">
                          {showSelectionSummary && allSelectedEntries.length > 0 ? (
                            <Button
                              type="button"
                              variant="ghost"
                              onClick={() => selectionHeading.current?.focus()}
                            >
                              {t('common.bindingGuide.reviewSelected', {
                                count: allSelectedEntries.length,
                              })}
                            </Button>
                          ) : (
                            <span>
                              {t('common.operations.charity.selectedCount', {
                                count: allSelectedEntries.length,
                              })}
                            </span>
                          )}
                          <div className="nb-actions">
                            <Button
                              type="button"
                              disabled={
                                candidateBusy ||
                                remainingOnPage === 0 ||
                                remainingOnPage > capacity - allSelectedEntries.length
                              }
                              onClick={() => {
                                const next = { ...selected };
                                for (const entry of selectable)
                                  next[charitySelectionKey(entry)] ??= selectCandidate(entry);
                                onChange(next);
                              }}
                            >
                              {t('common.workflow.selectPage', {
                                count: selectable.length,
                              })}
                            </Button>
                            <Button
                              type="button"
                              disabled={candidateBusy || !selectedOnPage}
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
                      )}
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
                          <ul className="nb-choice-list__items binding-model-options">
                            {candidatePage.data.map((entry) => {
                              const key = charitySelectionKey(entry);
                              return (
                                <li key={key}>
                                  <BindingModelChoice
                                    entry={entry}
                                    selected={Boolean(selected[key])}
                                    disabled={
                                      interactionBlocked ||
                                      candidates.isPlaceholderData ||
                                      Boolean(entry.current_binding_id) ||
                                      !entry.capabilities.can_bind ||
                                      (!selected[key] && allSelectedEntries.length >= capacity)
                                    }
                                    onSelect={(checked) => {
                                      const next = { ...selected };
                                      if (checked) next[key] = selectCandidate(entry);
                                      else delete next[key];
                                      onChange(next);
                                    }}
                                    inspect={
                                      !interactionBlocked && (onInspectBindings || renderKeyManager)
                                        ? () => {
                                            if (onInspectBindings) onInspectBindings();
                                            else {
                                              remember();
                                              setManagingKey(true);
                                            }
                                          }
                                        : undefined
                                    }
                                  />
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
        </div>
        {showSelectionSummary ? (
          <section
            className="charity-binding-selection"
            data-empty={allSelectedEntries.length === 0}
            aria-label={t('common.operations.charity.selectedModels')}
          >
            <h4 ref={selectionHeading} tabIndex={-1}>
              {t('common.operations.charity.selectedCount', {
                count: Object.keys(selected).length,
              })}
            </h4>

            <p role="status" aria-live="polite">
              {allSelectedEntries.length === 0
                ? fixedKey
                  ? t('common.bindingGuide.fixedSelectionHelp')
                  : t('common.operations.charity.crossSelectionHelp')
                : t('common.workflow.selectionCapacity', {
                    selected: allSelectedEntries.length,
                    capacity,
                    outside: allSelectedEntries.filter(
                      ([key]) =>
                        !candidatePage?.data.some((entry) => charitySelectionKey(entry) === key),
                    ).length,
                  })}
            </p>
            {allSelectedEntries.length > 0 && (
              <>
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
                  disabled={interactionBlocked}
                  onClick={() => {
                    setSelectedSearch('');
                    selectionHeading.current?.focus();
                    onChange({});
                  }}
                >
                  {t('common.workflow.clearSelected')}
                </Button>
                <ul className="charity-binding-selection__list">
                  {selectedPageEntries.map(([key, entry]) => (
                    <li key={key}>
                      <div className="charity-binding-selection__metadata">
                        <strong>
                          {allSelectedEntries.findIndex(([id]) => id === key) + 1}.{' '}
                          {entry.upstream_model_id}
                        </strong>
                        <span>
                          {t('common.operations.charity.donationNumber', { id: entry.donation_id })}{' '}
                          · {entry.note} · {entry.source.display_head}…{entry.source.display_tail}
                        </span>
                      </div>
                      <Button
                        type="button"
                        variant="ghost"
                        aria-label={t('common.workflow.removeSelection', {
                          id: entry.upstream_model_id,
                        })}
                        disabled={interactionBlocked}
                        onClick={() => {
                          const next = { ...selected };
                          delete next[key];
                          if (Object.keys(next).length) selectedSearchInput.current?.focus();
                          else {
                            setSelectedSearch('');
                            selectionHeading.current?.focus();
                          }
                          onChange(next);
                        }}
                      >
                        {t('common.operations.charity.remove')}
                      </Button>
                      {entry.native_services ? (
                        <BindingServiceEditor
                          guide={entry.binding_guide}
                          source={entry}
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
              </>
            )}
          </section>
        ) : null}
      </div>
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
