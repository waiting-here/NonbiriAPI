import { useCallback, useEffect, useRef, useState } from 'react';
import { useDetailNavigation } from '@shared/operations/useDetailNavigation';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useSearchState } from '@shared/operations/useSearchState';
import { useTranslation } from 'react-i18next';
import { DonationKeyNotes } from './DonationControlFacts';
import { clearStationSession } from '@shared/charityManagement';
import { charityKeys } from '@shared/operations/charity';
import {
  getDonationSourceKeysPage,
  getDonationSourcesPage,
  type DonationPageRole,
  type DonationPageSafeSource,
  type DonationSourceKeysPageFilters,
  type DonationSourcePageFilters,
  type DonationSourceSummary,
  type ManagedDonationKeySummary,
} from '@shared/operations/donationPages';
import { PagePagination } from '@shared/operations/PagePagination';
import { useUrlPagePager } from '@shared/operations/useUrlPagePager';
import { isForbidden, isNotFoundError, isUnauthorized } from '@shared/query/http';
import { useDateTimeFormatter } from '@shared/utils/datetime';
import { DonationHandlingStatus } from './DonationHandling';
import { KeyLimitSummary } from './KeyRoutingLimits';
import { FailureResetControl } from './FailureResetControl';
import { copyForRecurringLimits } from './recurringLimitsCopy';
import { Card, EmptyState, ErrorState, LoadingState, StatusBadge } from './States';
import '@shared/operations/operations.css';
import './CharitySourceBrowser.css';

export interface CharitySourceBrowserProps {
  charityModelID?: string;
  role: DonationPageRole;
  accountId: string;
  enabled: boolean;
  onOpenDonation: (donationId: string, keyId?: string) => void;
  onCapabilityLoss?: () => void;
}

type SourceScope = 'active' | 'all';
type HandlingFilter = '' | 'pending';
type IdleFilter = '' | 'yes' | 'no';

const SOURCE_LIST_TYPE = 'charity-sources';
const SOURCE_KEYS_LIST_TYPE = 'charity-source-keys';
const SOURCE_PAGE_PARAM = 'sources_page';
const SOURCE_PAGE_SIZE_PARAM = 'sources_page_size';
const SOURCE_KEYS_PAGE_PARAM = 'source_keys_page';
const SOURCE_KEYS_PAGE_SIZE_PARAM = 'source_keys_page_size';
const SOURCE_KEY_PARAM = 'source_key';
const SCOPE_PARAM = 'source_scope';
const SOURCE_QUERY_PARAM = 'source_q';
const KEY_QUERY_PARAM = 'source_key_q';
const HANDLING_PARAM = 'source_handling';
const IDLE_PARAM = 'source_idle';

const SOURCE_KEY_PATTERN = /^dsg_[A-Za-z0-9_-]{43}$/;
const BASE64URL_ALPHABET = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_';

const keyStateLabel: Record<ManagedDonationKeySummary['charity_state'], string> = {
  pending: 'common.operations.charity.charityState.pending',
  available: 'common.operations.charity.charityState.available',
  disabled: 'common.operations.charity.charityState.disabled',
  suspended: 'common.operations.charity.charityState.suspended',
  exhausted: 'common.operations.charity.charityState.exhausted',
  expired: 'common.operations.charity.charityState.expired',
  ended: 'common.operations.charity.charityState.ended',
};

const endedReasonLabel: Record<NonNullable<ManagedDonationKeySummary['ended_reason']>, string> = {
  withdrawn: 'common.operations.charity.endedReasonValue.withdrawn',
  terminated: 'common.operations.charity.endedReasonValue.terminated',
  expired: 'common.operations.charity.endedReasonValue.expired',
  member_removed: 'common.operations.charity.endedReasonValue.memberRemoved',
  account_deleted: 'common.operations.charity.endedReasonValue.accountDeleted',
};

function stationForRole(role: DonationPageRole): 'admin' | 'user' {
  return role === 'admin' ? 'admin' : 'user';
}

function sourceBrowserKeys(role: DonationPageRole, accountId: string, modelID?: string) {
  const root = [...charityKeys.root(role), 'source-browser', accountId, modelID ?? ''] as const;
  return {
    root,
    sources: (
      q: string,
      scope: SourceScope,
      handling: HandlingFilter,
      page: string,
      pageSize: number,
    ) => [...root, 'sources', q, scope, handling, page, pageSize] as const,
    keys: (
      sourceKey: string,
      q: string,
      scope: SourceScope,
      handling: HandlingFilter,
      idle: IdleFilter,
      page: string,
      pageSize: number,
    ) => [...root, 'source-keys', sourceKey, q, scope, handling, idle, page, pageSize] as const,
  };
}

function sameQueryFamily(
  previous: readonly unknown[] | undefined,
  current: readonly unknown[],
): boolean {
  return (
    previous !== undefined &&
    previous.length === current.length &&
    current.slice(0, -2).every((value, index) => previous[index] === value)
  );
}

function singleParameter(searchParams: URLSearchParams, name: string): string | undefined {
  const values = searchParams.getAll(name);
  return values.length === 1 ? values[0] : undefined;
}

function validSearch(value: string): boolean {
  if (Array.from(value).length > 128 || new TextEncoder().encode(value).byteLength > 512) {
    return false;
  }
  return Array.from(value).every((character) => {
    const codePoint = character.codePointAt(0) ?? 0;
    return (
      codePoint >= 0x20 &&
      !(codePoint >= 0x7f && codePoint <= 0x9f) &&
      !(codePoint >= 0xd800 && codePoint <= 0xdfff)
    );
  });
}

function isCanonicalSourceKey(value: string): boolean {
  if (!SOURCE_KEY_PATTERN.test(value)) return false;
  const finalCharacter = value[value.length - 1] ?? '';
  const finalValue = BASE64URL_ALPHABET.indexOf(finalCharacter);
  return finalValue >= 0 && finalValue % 4 === 0;
}

function readSearchParameter(
  searchParams: URLSearchParams,
  name: string,
): { value: string; needsNormalization: boolean } {
  const values = searchParams.getAll(name);
  if (values.length === 0) return { value: '', needsNormalization: false };
  if (values.length !== 1) return { value: '', needsNormalization: true };
  const rawValue = values[0] ?? '';
  if (!validSearch(rawValue)) return { value: '', needsNormalization: true };
  const value = rawValue.trim();
  if (value === '') return { value: '', needsNormalization: true };
  return { value, needsNormalization: rawValue !== value };
}

function readScopeParameter(searchParams: URLSearchParams): {
  value: SourceScope;
  needsNormalization: boolean;
} {
  const values = searchParams.getAll(SCOPE_PARAM);
  if (values.length === 1 && (values[0] === 'active' || values[0] === 'all')) {
    return { value: values[0], needsNormalization: false };
  }
  return { value: 'active', needsNormalization: true };
}

function readEnumParameter<T extends string>(
  searchParams: URLSearchParams,
  name: string,
  allowed: readonly T[],
): { value: T | ''; needsNormalization: boolean } {
  const values = searchParams.getAll(name);
  if (values.length === 0) return { value: '', needsNormalization: false };
  if (values.length === 1 && allowed.includes(values[0] as T)) {
    return { value: values[0] as T, needsNormalization: false };
  }
  return { value: '', needsNormalization: true };
}

function resetPagerParams(
  next: URLSearchParams,
  pageParam: string,
  pageSizeParam: string,
  pageSize: number,
): void {
  next.delete(pageParam);
  next.set(pageParam, '1');
  next.delete(pageSizeParam);
  next.set(pageSizeParam, String(pageSize));
}

function sourceLabel(
  source: DonationPageSafeSource,
  t: (key: string, options?: Record<string, unknown>) => string,
): string {
  return source.kind === 'custom'
    ? t('common.operations.charity.sourceBrowser.customSource', { baseUrl: source.base_url })
    : t('common.operations.charity.sourceBrowser.mainstreamSource', { name: source.name });
}

function keyReason(
  key: ManagedDonationKeySummary,
  t: (key: string, options?: Record<string, unknown>) => string,
): string | null {
  if (!key.physical_enabled)
    return t('common.operations.charity.sourceBrowser.physicalDisabledReason');
  if (key.streak.failure_disabled)
    return t('common.operations.charity.sourceBrowser.failureDisabledReason');
  if (key.charity_state === 'available') return null;
  if (key.charity_state === 'ended' && key.ended_reason !== null) {
    return t('common.operations.charity.sourceBrowser.endedReason', {
      reason: t(endedReasonLabel[key.ended_reason]),
    });
  }
  return t(`common.operations.charity.sourceBrowser.stateReason.${key.charity_state}`);
}

function UsageLimit({
  label,
  used,
  inflight,
  limit,
}: {
  label: string;
  used: string;
  inflight: string;
  limit: string | null;
}) {
  const { t } = useTranslation();
  return (
    <div>
      <dt>{label}</dt>
      <dd>
        {t('common.operations.charity.usageLimit', {
          used,
          inflight,
          limit: limit ?? t('common.operations.charity.unlimited'),
        })}
      </dd>
    </div>
  );
}

function SourceSummary({
  source,
  selected,
  onSelect,
  disabled,
}: {
  source: DonationSourceSummary;
  selected: boolean;
  onSelect: () => void;
  disabled: boolean;
}) {
  const { t } = useTranslation();
  return (
    <article className={`charity-source-browser__source${selected ? ' is-selected' : ''}`}>
      <button
        type="button"
        className="charity-source-browser__source-button"
        aria-current={selected ? 'true' : undefined}
        disabled={disabled}
        onClick={onSelect}
      >
        <span className="charity-source-browser__source-heading">
          <strong>{sourceLabel(source.safe_source, t)}</strong>
          <span>{source.safe_source.connector_type}</span>
        </span>
        <span className="charity-source-browser__source-address">
          {source.safe_source.base_url}
        </span>
        <dl className="charity-source-browser__source-counts">
          <div>
            <dt>{t('common.operations.charity.sourceBrowser.donations')}</dt>
            <dd>{source.donation_count}</dd>
          </div>
          <div>
            <dt>{t('common.operations.charity.sourceBrowser.keys')}</dt>
            <dd>{source.key_count}</dd>
          </div>
          <div>
            <dt>{t('common.operations.charity.sourceBrowser.usableKeys')}</dt>
            <dd>{source.usable_key_count}</dd>
          </div>
          <div>
            <dt>{t('common.operations.charity.sourceBrowser.pendingDonations')}</dt>
            <dd>{source.pending_donation_count}</dd>
          </div>
        </dl>
      </button>
    </article>
  );
}

function RecurringSummary({ keyValue }: { keyValue: ManagedDonationKeySummary }) {
  const { i18n, t } = useTranslation();
  const copy = copyForRecurringLimits(i18n.language);
  const total = Number(keyValue.rule_count);
  return (
    <details className="charity-source-browser__recurring">
      <summary>{copy.ruleCount(total)}</summary>
      {keyValue.rules.length === 0 ? (
        <p className="muted">{copy.noRules}</p>
      ) : (
        <ul>
          {keyValue.rules.map((rule) => (
            <li key={rule.id}>
              <strong>{rule.id ? copy.ruleId(rule.id) : copy.unsavedRule}</strong>
              <span>
                {copy.modeValue[rule.mode]} · {copy.intervalValue[rule.interval]}
              </span>
              <span>{copy.formatMetricValue(rule.limit, rule.metric)}</span>
              <span>{copy.state[rule.state]}</span>
              <small>
                {copy.used}: {rule.used} · {copy.reserved}: {rule.reserved} · {copy.remaining}:{' '}
                {rule.remaining}
              </small>
            </li>
          ))}
        </ul>
      )}
      {total > keyValue.rules.length ? (
        <p className="muted">
          {t('common.operations.charity.sourceBrowser.recurringMore', {
            shown: keyValue.rules.length,
            total,
          })}
        </p>
      ) : null}
    </details>
  );
}

function KeySummary({
  keyValue,
  onOpenDonation,
  disabled,
}: {
  keyValue: ManagedDonationKeySummary;
  onOpenDonation: CharitySourceBrowserProps['onOpenDonation'];
  disabled: boolean;
}) {
  const formatDateTime = useDateTimeFormatter();
  const { t } = useTranslation();
  const reason = keyReason(keyValue, t);
  return (
    <article className="charity-source-browser__key">
      <header className="charity-source-browser__key-heading">
        <div>
          <h4>
            {t('common.operations.charity.sourceBrowser.keyHeading', {
              head: keyValue.display_head,
              tail: keyValue.display_tail,
            })}
          </h4>
          <p className="muted">
            {t('common.operations.charity.sourceBrowser.sourceLine', {
              connector: keyValue.safe_source.connector_type,
              source: sourceLabel(keyValue.safe_source, t),
            })}
          </p>
        </div>
        <StatusBadge
          active={
            keyValue.physical_enabled &&
            !keyValue.streak.failure_disabled &&
            keyValue.charity_state === 'available'
          }
          danger={keyValue.charity_state === 'ended' || keyValue.charity_state === 'expired'}
          label={t(keyStateLabel[keyValue.charity_state])}
        />
      </header>
      <DonationKeyNotes
        note={keyValue.safe_note}
        donor={keyValue.donation_note}
        approval={keyValue.approval_note}
      />
      <dl className="ops-kv">
        <dt>{t('common.operations.charity.sourceBrowser.donation')}</dt>
        <dd className="ops-id">{keyValue.donation_id}</dd>
        <dt>{t('common.operations.charity.sourceBrowser.keyId')}</dt>
        <dd className="ops-id">{keyValue.key_id}</dd>
        <dt>{t('common.operations.charity.sourceBrowser.expiry')}</dt>
        <dd>
          {keyValue.expires_at === null
            ? t('common.operations.charity.noExpiry')
            : formatDateTime(keyValue.expires_at)}
        </dd>
        <dt>{t('common.operations.charity.sourceBrowser.physicalStatus')}</dt>
        <dd>
          {t(
            keyValue.physical_enabled
              ? 'common.operations.charity.sourceBrowser.physicalEnabled'
              : 'common.operations.charity.sourceBrowser.physicalDisabled',
          )}
        </dd>
        <dt>{t('common.operations.charity.sourceBrowser.bindingStatus')}</dt>
        <dd>
          {keyValue.idle
            ? t('common.donationHandling.idle')
            : t('common.donationHandling.bound', { count: keyValue.binding_count })}
        </dd>
      </dl>
      {reason ? <p className="inline-notice">{reason}</p> : null}
      <div className="charity-source-browser__handling">
        <span>{t('common.operations.charity.sourceBrowser.handling')}</span>
        <DonationHandlingStatus handling={keyValue.handling} />
      </div>
      <section
        className="charity-source-browser__limits"
        aria-label={t('common.operations.charity.sourceBrowser.physicalLimits')}
      >
        <h5>{t('common.operations.charity.sourceBrowser.physicalLimits')}</h5>
        <KeyLimitSummary concurrency={keyValue.max_concurrency} rpm={keyValue.max_rpm} readOnly />
      </section>
      <section
        className="charity-source-browser__limits"
        aria-label={t('common.operations.charity.sourceBrowser.charityLimits')}
      >
        <h5>{t('common.operations.charity.sourceBrowser.charityLimits')}</h5>
        <dl className="ops-kv">
          <UsageLimit
            label={t('common.operations.charity.priceLimitCredits')}
            used={keyValue.usage.price_used}
            inflight={keyValue.usage.price_inflight}
            limit={keyValue.limits.price}
          />
          <UsageLimit
            label={t('common.operations.charity.callLimit')}
            used={keyValue.usage.calls_used}
            inflight={keyValue.usage.calls_inflight}
            limit={keyValue.limits.calls}
          />
          <UsageLimit
            label={t('common.operations.charity.tokenLimit')}
            used={keyValue.usage.tokens_used}
            inflight={keyValue.usage.tokens_inflight}
            limit={keyValue.limits.tokens}
          />
        </dl>
      </section>
      <RecurringSummary keyValue={keyValue} />
      <div className="ops-actions charity-source-browser__actions">
        <button
          type="button"
          className="btn btn-secondary"
          disabled={disabled}
          onClick={() => onOpenDonation(keyValue.donation_id)}
        >
          {t('common.operations.charity.sourceBrowser.manageDonation', {
            id: keyValue.donation_id,
          })}
        </button>
        <button
          type="button"
          className="btn btn-primary"
          disabled={disabled}
          onClick={() => onOpenDonation(keyValue.donation_id, keyValue.key_id)}
        >
          {t('common.operations.charity.sourceBrowser.manageKey', { id: keyValue.key_id })}
        </button>
      </div>
    </article>
  );
}

export function CharitySourceBrowser({
  role,
  accountId,
  charityModelID,
  enabled,
  onOpenDonation,
  onCapabilityLoss,
}: CharitySourceBrowserProps) {
  const { t } = useTranslation();
  const client = useQueryClient();
  const [searchParams, setSearchParams] = useSearchState();
  const sourceSearch = readSearchParameter(searchParams, SOURCE_QUERY_PARAM);
  const keySearch = readSearchParameter(searchParams, KEY_QUERY_PARAM);
  const scope = readScopeParameter(searchParams);
  const handling = readEnumParameter(searchParams, HANDLING_PARAM, ['pending'] as const);
  const idle = readEnumParameter(searchParams, IDLE_PARAM, ['yes', 'no'] as const);
  const rawSourceKey = singleParameter(searchParams, SOURCE_KEY_PARAM) ?? '';
  const sourceKey = isCanonicalSourceKey(rawSourceKey) ? rawSourceKey : '';
  const sourceKeyNeedsNormalization =
    searchParams.getAll(SOURCE_KEY_PARAM).length > 0 && sourceKey === '';
  const station = stationForRole(role);
  const identity = `${role}\0${accountId}`;
  const [observedIdentity, setObservedIdentity] = useState(identity);
  const identityTransitioning = observedIdentity !== identity;
  const [revoked, setRevoked] = useState(false);
  const capabilityNotified = useRef(false);
  const identityRef = useRef(identity);
  const [sourceSearchDraft, setSourceSearchDraft] = useState(sourceSearch.value);
  const [keySearchDraft, setKeySearchDraft] = useState(keySearch.value);
  const [sourceSearchError, setSourceSearchError] = useState(false);
  const [keySearchError, setKeySearchError] = useState(false);
  const sourcePager = useUrlPagePager({
    station,
    listType: SOURCE_LIST_TYPE,
    scopeKey: accountId,
    scopeReady: enabled && accountId.length > 0,
    pageParam: SOURCE_PAGE_PARAM,
    pageSizeParam: SOURCE_PAGE_SIZE_PARAM,
  });
  const sourceKeysPager = useUrlPagePager({
    station,
    listType: SOURCE_KEYS_LIST_TYPE,
    scopeKey: accountId,
    scopeReady: enabled && accountId.length > 0,
    pageParam: SOURCE_KEYS_PAGE_PARAM,
    pageSizeParam: SOURCE_KEYS_PAGE_SIZE_PARAM,
  });
  const browserKeys = sourceBrowserKeys(role, accountId, charityModelID);
  const readEnabled = enabled && accountId.length > 0 && !identityTransitioning && !revoked;
  const sourceFilters: DonationSourcePageFilters = {
    q: sourceSearch.value,
    scope: scope.value,
    ...(handling.value ? { handling: handling.value } : {}),
  };
  const sourceKeysFilters: DonationSourceKeysPageFilters = {
    q: keySearch.value,
    scope: scope.value,
    ...(handling.value ? { handling: handling.value } : {}),
    ...(idle.value ? { idle: idle.value } : {}),
  };
  const sources = useQuery({
    queryKey: browserKeys.sources(
      sourceSearch.value,
      scope.value,
      handling.value,
      sourcePager.page,
      sourcePager.pageSize,
    ),
    queryFn: ({ signal }) =>
      getDonationSourcesPage(
        role,
        sourceFilters,
        sourcePager.page,
        sourcePager.pageSize,
        signal,
        charityModelID,
      ),
    enabled: readEnabled,
    retry: false,
    placeholderData: (previous, previousQuery) =>
      sameQueryFamily(
        previousQuery?.queryKey,
        browserKeys.sources(
          sourceSearch.value,
          scope.value,
          handling.value,
          sourcePager.page,
          sourcePager.pageSize,
        ),
      )
        ? previous
        : undefined,
  });
  const selectedSource = sources.data?.data.find((entry) => entry.source_key === sourceKey);
  const sourceKeys = useQuery({
    queryKey: browserKeys.keys(
      sourceKey,
      keySearch.value,
      scope.value,
      handling.value,
      idle.value,
      sourceKeysPager.page,
      sourceKeysPager.pageSize,
    ),
    queryFn: ({ signal }) =>
      getDonationSourceKeysPage(
        role,
        sourceKey,
        sourceKeysFilters,
        sourceKeysPager.page,
        sourceKeysPager.pageSize,
        signal,
        charityModelID,
      ),
    enabled: readEnabled && sourceKey !== '',
    retry: false,
    placeholderData: (previous, previousQuery) =>
      sameQueryFamily(
        previousQuery?.queryKey,
        browserKeys.keys(
          sourceKey,
          keySearch.value,
          scope.value,
          handling.value,
          idle.value,
          sourceKeysPager.page,
          sourceKeysPager.pageSize,
        ),
      )
        ? previous
        : undefined,
  });
  const authorityError = [sources.error, sourceKeys.error].find(
    (error) => isUnauthorized(error) || isForbidden(error),
  );
  const navigationReady = sourceKey
    ? !sources.isPending && !sources.isFetching && !sourceKeys.isPending && !sourceKeys.isFetching
    : !sources.isPending && !sources.isFetching;
  const { listRef, detailRef, remember } = useDetailNavigation<HTMLElement, HTMLElement>(
    sourceKey,
    navigationReady,
  );

  useEffect(() => {
    if (identityRef.current === identity) return;
    const [previousRole, previousAccountId] = identityRef.current.split('\0') as [
      DonationPageRole,
      string,
    ];
    identityRef.current = identity;
    setObservedIdentity(identity);
    setRevoked(false);
    capabilityNotified.current = false;
    setSourceSearchDraft('');
    setKeySearchDraft('');
    setSourceSearchError(false);
    setKeySearchError(false);
    void client.cancelQueries({
      queryKey: sourceBrowserKeys(previousRole, previousAccountId).root,
    });
    client.removeQueries({ queryKey: sourceBrowserKeys(previousRole, previousAccountId).root });
    setSearchParams((previous) => {
      const next = new URLSearchParams(previous);
      for (const name of [
        SOURCE_PAGE_PARAM,
        SOURCE_PAGE_SIZE_PARAM,
        SOURCE_KEYS_PAGE_PARAM,
        SOURCE_KEYS_PAGE_SIZE_PARAM,
        SOURCE_KEY_PARAM,
        SOURCE_QUERY_PARAM,
        KEY_QUERY_PARAM,
        IDLE_PARAM,
      ]) {
        next.delete(name);
      }
      return next;
    });
  }, [client, identity, setSearchParams]);

  /* eslint-disable react-hooks/set-state-in-effect */
  useEffect(() => {
    setSourceSearchDraft(sourceSearch.value);
    setSourceSearchError(false);
  }, [sourceSearch.value]);

  useEffect(() => {
    setKeySearchDraft(keySearch.value);
    setKeySearchError(false);
  }, [keySearch.value]);
  /* eslint-enable react-hooks/set-state-in-effect */

  useEffect(() => {
    if (
      !sourceSearch.needsNormalization &&
      !keySearch.needsNormalization &&
      !scope.needsNormalization &&
      !handling.needsNormalization &&
      !idle.needsNormalization &&
      !sourceKeyNeedsNormalization
    )
      return;
    setSearchParams(
      (previous) => {
        const next = new URLSearchParams(previous);
        if (sourceSearch.needsNormalization) {
          next.delete(SOURCE_QUERY_PARAM);
          if (sourceSearch.value) next.set(SOURCE_QUERY_PARAM, sourceSearch.value);
        }
        if (keySearch.needsNormalization) {
          next.delete(KEY_QUERY_PARAM);
          if (keySearch.value) next.set(KEY_QUERY_PARAM, keySearch.value);
        }
        if (scope.needsNormalization) {
          next.delete(SCOPE_PARAM);
          next.set(SCOPE_PARAM, scope.value);
        }
        if (handling.needsNormalization) {
          next.delete(HANDLING_PARAM);
          if (handling.value) next.set(HANDLING_PARAM, handling.value);
        }
        if (idle.needsNormalization) {
          next.delete(IDLE_PARAM);
          if (idle.value) next.set(IDLE_PARAM, idle.value);
        }
        if (sourceKeyNeedsNormalization) {
          next.delete(SOURCE_KEY_PARAM);
          next.delete(SOURCE_KEYS_PAGE_PARAM);
          next.delete(SOURCE_KEYS_PAGE_SIZE_PARAM);
        }
        return next;
      },
      { replace: true },
    );
  }, [
    handling.needsNormalization,
    handling.value,
    idle.needsNormalization,
    idle.value,
    keySearch.needsNormalization,
    keySearch.value,
    scope.needsNormalization,
    scope.value,
    setSearchParams,
    sourceKeyNeedsNormalization,
    sourceSearch.needsNormalization,
    sourceSearch.value,
  ]);

  useEffect(() => {
    if (!authorityError || capabilityNotified.current) return;
    capabilityNotified.current = true;
    setRevoked(true);
    clearStationSession(client, role);
    setSearchParams((previous) => {
      const next = new URLSearchParams(previous);
      for (const name of [
        SOURCE_PAGE_PARAM,
        SOURCE_PAGE_SIZE_PARAM,
        SOURCE_KEYS_PAGE_PARAM,
        SOURCE_KEYS_PAGE_SIZE_PARAM,
        SOURCE_KEY_PARAM,
        SOURCE_QUERY_PARAM,
        KEY_QUERY_PARAM,
        IDLE_PARAM,
      ]) {
        next.delete(name);
      }
      return next;
    });
    onCapabilityLoss?.();
  }, [authorityError, client, onCapabilityLoss, role, setSearchParams]);

  const updateSearch = useCallback(
    (name: string, value: string, resetOuter: boolean, resetInner: boolean) => {
      setSearchParams((previous) => {
        const next = new URLSearchParams(previous);
        next.delete(name);
        if (value) next.set(name, value);
        if (resetOuter)
          resetPagerParams(next, SOURCE_PAGE_PARAM, SOURCE_PAGE_SIZE_PARAM, sourcePager.pageSize);
        if (resetInner)
          resetPagerParams(
            next,
            SOURCE_KEYS_PAGE_PARAM,
            SOURCE_KEYS_PAGE_SIZE_PARAM,
            sourceKeysPager.pageSize,
          );
        return next;
      });
    },
    [setSearchParams, sourceKeysPager.pageSize, sourcePager.pageSize],
  );

  const clearSource = useCallback(() => {
    setSearchParams((previous) => {
      const next = new URLSearchParams(previous);
      next.delete(SOURCE_KEY_PARAM);
      next.delete(SOURCE_KEYS_PAGE_PARAM);
      next.delete(SOURCE_KEYS_PAGE_SIZE_PARAM);
      return next;
    });
  }, [setSearchParams]);

  const selectSource = useCallback(
    (nextSourceKey: string) => {
      remember();
      setSearchParams((previous) => {
        const next = new URLSearchParams(previous);
        next.delete(SOURCE_KEY_PARAM);
        next.set(SOURCE_KEY_PARAM, nextSourceKey);
        resetPagerParams(
          next,
          SOURCE_KEYS_PAGE_PARAM,
          SOURCE_KEYS_PAGE_SIZE_PARAM,
          sourceKeysPager.pageSize,
        );
        return next;
      });
    },
    [setSearchParams, sourceKeysPager.pageSize, remember],
  );

  const sourceBusy = sources.isFetching;
  const sourceKeysBusy = sourceKeys.isFetching;
  if (!enabled || accountId.length === 0) return null;
  if (revoked || authorityError) {
    return (
      <Card className="charity-source-browser">
        <p className="field-error" role="alert">
          {t('common.operations.charity.accessLost')}
        </p>
      </Card>
    );
  }
  if (identityTransitioning) return <LoadingState />;

  const sourcePage = sources.data;
  const keyPage = sourceKeys.data;
  const sourceNotFound = isNotFoundError(sourceKeys.error);
  const selectedKeySource = keyPage?.data[0]?.safe_source;
  const selectedSourceTitle = selectedSource
    ? sourceLabel(selectedSource.safe_source, t)
    : selectedKeySource
      ? sourceLabel(selectedKeySource, t)
      : t('common.operations.charity.sourceBrowser.sourceKeys');

  return (
    <Card className="charity-source-browser">
      <header className="charity-source-browser__header">
        <div>
          <h2>{t('common.operations.charity.sourceBrowser.title')}</h2>
          <p>{t('common.operations.charity.sourceBrowser.description')}</p>
        </div>
        {sourceKey ? (
          <button type="button" className="btn btn-quiet" onClick={clearSource}>
            {t('common.operations.charity.sourceBrowser.backToSources')}
          </button>
        ) : null}
      </header>
      <div className="charity-source-browser__filters">
        <form
          className="ops-toolbar"
          onSubmit={(event) => {
            event.preventDefault();
            if (!validSearch(sourceSearchDraft)) {
              setSourceSearchError(true);
              return;
            }
            setSourceSearchError(false);
            updateSearch(SOURCE_QUERY_PARAM, sourceSearchDraft.trim(), true, true);
          }}
        >
          <label>
            <span>{t('common.operations.charity.sourceBrowser.searchSources')}</span>
            <input
              type="search"
              value={sourceSearchDraft}
              maxLength={256}
              aria-invalid={sourceSearchError}
              aria-describedby={
                sourceSearchError ? 'charity-source-browser-source-search-error' : undefined
              }
              onChange={(event) => {
                setSourceSearchDraft(event.target.value);
                setSourceSearchError(false);
              }}
            />
            {sourceSearchError ? (
              <span
                id="charity-source-browser-source-search-error"
                className="field-error"
                role="alert"
              >
                {t('common.operations.charity.sourceBrowser.searchInvalid')}
              </span>
            ) : null}
          </label>
          <button type="submit" className="btn btn-secondary">
            {t('common.operations.charity.sourceBrowser.applySearch')}
          </button>
          {sourceSearch.value ? (
            <button
              type="button"
              className="btn btn-quiet"
              onClick={() => {
                setSourceSearchDraft('');
                updateSearch(SOURCE_QUERY_PARAM, '', true, true);
              }}
            >
              {t('common.operations.charity.sourceBrowser.clearSearch')}
            </button>
          ) : null}
          <label>
            <span>{t('common.operations.charity.sourceBrowser.scope')}</span>
            <select
              value={scope.value}
              onChange={(event) => updateSearch(SCOPE_PARAM, event.target.value, true, true)}
            >
              <option value="active">{t('common.operations.charity.sourceBrowser.active')}</option>
              <option value="all">{t('common.operations.charity.sourceBrowser.all')}</option>
            </select>
          </label>
          <label>
            <span>{t('common.operations.charity.sourceBrowser.handlingFilter')}</span>
            <select
              value={handling.value}
              onChange={(event) => updateSearch(HANDLING_PARAM, event.target.value, true, true)}
            >
              <option value="">{t('common.donationHandling.all')}</option>
              <option value="pending">
                {t('common.operations.charity.sourceBrowser.pendingOnly')}
              </option>
            </select>
          </label>
        </form>
      </div>
      {sourceBusy && sourcePage ? <LoadingState label={t('common.loading')} /> : null}
      <div className={`charity-source-browser__columns${sourceKey ? ' has-selection' : ''}`}>
        <section
          className="charity-source-browser__sources"
          ref={listRef}
          tabIndex={-1}
          aria-label={t('common.operations.charity.sourceBrowser.sources')}
        >
          <h3>{t('common.operations.charity.sourceBrowser.sources')}</h3>
          {sourcePage ? (
            <FailureResetControl
              key={JSON.stringify([role, accountId, sourceFilters])}
              role={role}
              selection={{ view: 'sources', ...sourceFilters }}
              disabled={sourceBusy || Boolean(sources.error)}
              onCapabilityLoss={onCapabilityLoss}
              choices={sourcePage.data.map((item) => ({
                id: item.source_key,
                label: sourceLabel(item.safe_source, t),
                target: { view: 'source_keys', source_key: item.source_key, ...sourceFilters },
              }))}
            />
          ) : null}
          {sources.isPending ? (
            <LoadingState />
          ) : sources.error ? (
            <ErrorState error={sources.error} onRetry={() => void sources.refetch()} />
          ) : sourcePage?.data.length === 0 ? (
            <EmptyState
              title={t('common.operations.charity.sourceBrowser.noSources')}
              body={t('common.operations.charity.sourceBrowser.noSourcesBody')}
            />
          ) : sourcePage ? (
            <div className="charity-source-browser__source-list">
              {sourcePage.data.map((source) => (
                <SourceSummary
                  key={source.source_key}
                  source={source}
                  selected={source.source_key === sourceKey}
                  disabled={sourceBusy || sourceKeysBusy}
                  onSelect={() => selectSource(source.source_key)}
                />
              ))}
            </div>
          ) : null}
          {!sources.isPending && !sources.error && sourcePage ? (
            <PagePagination
              metadata={sourcePage.pagination}
              requestedPage={sourcePager.page}
              onPageChange={sourcePager.setPage}
              onPageSizeChange={sourcePager.setPageSize}
              busy={sourceBusy}
            />
          ) : null}
        </section>
        <section
          className="charity-source-browser__keys ops-detail-target"
          ref={detailRef}
          tabIndex={-1}
          aria-label={t('common.operations.charity.sourceBrowser.sourceKeys')}
        >
          {!sourceKey ? (
            <EmptyState
              title={t('common.operations.charity.sourceBrowser.chooseSource')}
              body={t('common.operations.charity.sourceBrowser.chooseSourceBody')}
            />
          ) : sourceNotFound ? (
            <div className="charity-source-browser__missing" role="status">
              <h3>{t('common.operations.charity.sourceBrowser.sourceMissing')}</h3>
              <p>{t('common.operations.charity.sourceBrowser.sourceMissingBody')}</p>
              <button type="button" className="btn btn-secondary" onClick={clearSource}>
                {t('common.operations.charity.sourceBrowser.backToSources')}
              </button>
            </div>
          ) : (
            <>
              <header className="charity-source-browser__keys-header">
                <div>
                  <h3>{selectedSourceTitle}</h3>
                </div>
                <button type="button" className="btn btn-quiet" onClick={clearSource}>
                  {t('common.operations.charity.sourceBrowser.backToSources')}
                </button>
              </header>
              <form
                className="ops-toolbar"
                onSubmit={(event) => {
                  event.preventDefault();
                  if (!validSearch(keySearchDraft)) {
                    setKeySearchError(true);
                    return;
                  }
                  setKeySearchError(false);
                  updateSearch(KEY_QUERY_PARAM, keySearchDraft.trim(), false, true);
                }}
              >
                <label>
                  <span>{t('common.operations.charity.sourceBrowser.searchKeys')}</span>
                  <input
                    type="search"
                    value={keySearchDraft}
                    maxLength={256}
                    aria-invalid={keySearchError}
                    aria-describedby={
                      keySearchError ? 'charity-source-browser-key-search-error' : undefined
                    }
                    onChange={(event) => {
                      setKeySearchDraft(event.target.value);
                      setKeySearchError(false);
                    }}
                  />
                  {keySearchError ? (
                    <span
                      id="charity-source-browser-key-search-error"
                      className="field-error"
                      role="alert"
                    >
                      {t('common.operations.charity.sourceBrowser.searchInvalid')}
                    </span>
                  ) : null}
                </label>
                <button type="submit" className="btn btn-secondary">
                  {t('common.operations.charity.sourceBrowser.applySearch')}
                </button>
                {keySearch.value ? (
                  <button
                    type="button"
                    className="btn btn-quiet"
                    onClick={() => {
                      setKeySearchDraft('');
                      updateSearch(KEY_QUERY_PARAM, '', false, true);
                    }}
                  >
                    {t('common.operations.charity.sourceBrowser.clearSearch')}
                  </button>
                ) : null}
                <label>
                  <span>{t('common.operations.charity.sourceBrowser.idleFilter')}</span>
                  <select
                    value={idle.value}
                    onChange={(event) => updateSearch(IDLE_PARAM, event.target.value, false, true)}
                  >
                    <option value="">
                      {t('common.operations.charity.sourceBrowser.allBindingStates')}
                    </option>
                    <option value="yes">
                      {t('common.operations.charity.sourceBrowser.idleOnly')}
                    </option>
                    <option value="no">
                      {t('common.operations.charity.sourceBrowser.boundOnly')}
                    </option>
                  </select>
                </label>
              </form>
              {keyPage ? (
                <FailureResetControl
                  key={JSON.stringify([role, accountId, sourceKey, sourceKeysFilters])}
                  role={role}
                  selection={{ view: 'source_keys', source_key: sourceKey, ...sourceKeysFilters }}
                  disabled={sourceKeysBusy || Boolean(sourceKeys.error)}
                  onCapabilityLoss={onCapabilityLoss}
                  choices={keyPage.data.map((item) => ({
                    id: item.key_id,
                    label: `${item.donation_id} / ${item.key_id} · ${item.display_head}…${item.display_tail}`,
                    target: {
                      donation_id: item.donation_id,
                      key_id: item.key_id,
                      expected_revision: item.donation_revision,
                    },
                  }))}
                />
              ) : null}
              {sourceKeysBusy && keyPage ? <LoadingState label={t('common.loading')} /> : null}
              {sourceKeys.isPending ? (
                <LoadingState />
              ) : sourceKeys.error ? (
                <ErrorState error={sourceKeys.error} onRetry={() => void sourceKeys.refetch()} />
              ) : keyPage?.data.length === 0 ? (
                <EmptyState
                  title={t('common.operations.charity.sourceBrowser.noKeys')}
                  body={t('common.operations.charity.sourceBrowser.noKeysBody')}
                />
              ) : keyPage ? (
                <div className="charity-source-browser__key-list">
                  {keyPage.data.map((keyValue) => (
                    <KeySummary
                      key={keyValue.key_id}
                      keyValue={keyValue}
                      disabled={sourceBusy || sourceKeysBusy || Boolean(sources.error)}
                      onOpenDonation={onOpenDonation}
                    />
                  ))}
                </div>
              ) : null}
              {!sourceKeys.isPending && !sourceKeys.error && keyPage ? (
                <PagePagination
                  metadata={keyPage.pagination}
                  requestedPage={sourceKeysPager.page}
                  onPageChange={sourceKeysPager.setPage}
                  onPageSizeChange={sourceKeysPager.setPageSize}
                  busy={sourceKeysBusy}
                />
              ) : null}
            </>
          )}
        </section>
      </div>
    </Card>
  );
}
