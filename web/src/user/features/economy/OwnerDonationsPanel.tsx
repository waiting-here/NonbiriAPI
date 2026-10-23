import {
  DONATION_DISPLAY_STATUSES,
  type DonationDisplayStatus,
  type UseSummary as DonationUseSummary,
} from '@shared/operations/donationUses';
import { DonationStatusBadge, UseSummary } from '@shared/components/UseSummary';
import { DonationUsesPanel } from '@shared/components/DonationUsesPanel';
import { smartBackoffDeadline } from '@shared/operations/smartBackoff';
import { useEffect, useState } from 'react';
import { useQuery, useQueryClient, type QueryKey } from '@tanstack/react-query';
import { Link, useLocation } from 'react-router';
import { useSearchState } from '@shared/operations/useSearchState';
import { useTranslation } from 'react-i18next';
import { EmptyState, ErrorState, LoadingState } from '@shared/components/States';
import { PagePagination } from '@shared/operations/PagePagination';
import { useUrlPagePager } from '@shared/operations/useUrlPagePager';
import { formatDateTime } from '@shared/utils/datetime';
import { isForbidden, isUnauthorized } from '@shared/query/http';
import { isStationSessionChanged } from '@shared/charityManagement';
import { listReturnPath } from '@shared/operations/listReturn';
import { copyForRecurringLimits } from '@shared/components/recurringLimitsCopy';
import { DonationKeyPanel } from './CharityPanels';
import { useRateLimitRefresh } from '@shared/operations/useRateLimitRefresh';
import { DonationThanks } from '@shared/components/DonationControlFacts';
import {
  Button,
  Field,
  FilterBar,
  Fold,
  MoreMenu,
  Panel,
  PanelBody,
  PanelHead,
  Tabs,
} from '@shared/components/ui';
import { DonationConnectorLabel } from './DonationConnectorLabel';
import { economyKeys, economySessionRequest } from './queries';
import {
  getOwnerDonationsPage,
  getOwnerDonationKeysPage,
  type OwnerDonationSummary,
  type OwnerDonationView,
  type OwnerDonationKeyView,
} from './donationPages';
import { DonationHistoryBatch, DonationHistoryToggle } from './DonationHistoryControls';
import { ExactCount } from './ExactValue';
import type { DonationStatus } from './types';

const VIEWS = ['current', 'history', 'hidden', 'all'] as const;
const viewLabel = {
  current: 'user.charity.ownerHistory.current',
  history: 'user.charity.ownerHistory.history',
  hidden: 'user.charity.ownerHistory.hidden',
  all: 'user.charity.ownerHistory.all',
} as const;
const keyViewLabel = {
  current: 'user.charity.ownerHistory.keys.current',
  ended: 'user.charity.ownerHistory.keys.ended',
  all: 'user.charity.ownerHistory.keys.all',
} as const;
const STATUSES = ['', ...DONATION_DISPLAY_STATUSES] as const;
const terminal = (status?: DonationStatus) =>
  status !== undefined && ['rejected', 'deleted', 'expired'].includes(status);
const single = (params: URLSearchParams, name: string) =>
  params.getAll(name).length === 1 ? params.get(name)! : '';
function searchValid(value: string): boolean {
  return (
    Array.from(value).length <= 128 &&
    Array.from(value).every((character) => {
      const point = character.codePointAt(0)!;
      return (
        point >= 0x20 && !(point >= 0x7f && point <= 0x9f) && !(point >= 0xd800 && point <= 0xdfff)
      );
    })
  );
}
function donationID(value: string): string {
  return /^[1-9][0-9]{0,18}$/.test(value) && BigInt(value) <= 9_223_372_036_854_775_807n
    ? value
    : '';
}
function inFamily(key: QueryKey | undefined, prefix: QueryKey): boolean {
  return Boolean(
    key &&
    key.length === prefix.length + 2 &&
    prefix.every((part, index) => Object.is(part, key[index])),
  );
}

const accessLost = (error: unknown) =>
  isUnauthorized(error) || isForbidden(error) || isStationSessionChanged(error);

export function OwnerDonationsPanel({
  accountID,
  enabled = true,
}: {
  accountID: string;
  enabled?: boolean;
}) {
  const [contextAccount, setContextAccount] = useState(accountID);
  const [params, setParams] = useSearchState();
  const changingAccount = contextAccount !== accountID;
  const accountParameters = [
    'donation_q',
    'donation_view',
    'donation_keys_view',
    'donation_status',
    'donation_expanded',
    'donations_page',
    'donation_keys_page',
  ];
  const needsAccountCleanup = accountParameters.some((name) => params.has(name));
  if (changingAccount && !needsAccountCleanup) setContextAccount(accountID);
  useEffect(() => {
    if (!changingAccount) return;
    const next = new URLSearchParams(params);
    for (const name of [
      'donation_q',
      'donation_view',
      'donation_keys_view',
      'donation_status',
      'donation_expanded',
      'donations_page',
      'donation_keys_page',
    ])
      next.delete(name);
    if (next.toString() !== params.toString()) setParams(next, { replace: true });
  }, [accountID, changingAccount, params, setParams]);
  if (changingAccount) return <LoadingState />;
  return <OwnerDonationsAccount key={accountID} accountID={accountID} enabled={enabled} />;
}

function OwnerDonationsAccount({ accountID, enabled }: { accountID: string; enabled: boolean }) {
  const { t } = useTranslation();
  const client = useQueryClient();
  const location = useLocation();
  const [params, setParams] = useSearchState();
  const rawQ = single(params, 'donation_q');
  const q = searchValid(rawQ) ? rawQ : '';
  const rawView = single(params, 'donation_view');
  const view: OwnerDonationView = VIEWS.includes(rawView as OwnerDonationView)
    ? (rawView as OwnerDonationView)
    : 'current';
  const statuses =
    view === 'current'
      ? ([
          '',
          'pending',
          'approved_pending',
          'approved',
          'partially_approved',
          'rejected',
          'withdrawn',
          'mixed',
        ] as const)
      : view === 'all'
        ? STATUSES
        : (['', 'rejected', 'deleted', 'expired'] as const);
  const rawStatus = single(params, 'donation_status');
  const status = (statuses as readonly string[]).includes(rawStatus)
    ? (rawStatus as DonationDisplayStatus | '')
    : '';
  const expanded = donationID(single(params, 'donation_expanded'));
  const [draft, setDraft] = useState({ source: q, value: q, invalid: false });
  const shownDraft = draft.source === q ? draft : { source: q, value: q, invalid: false };
  const pager = useUrlPagePager({
    station: 'user',
    listType: 'owner-donations',
    scopeKey: accountID,
    pageParam: 'donations_page',
    pageSizeParam: 'donations_page_size',
  });
  useEffect(() => {
    const next = new URLSearchParams(params);
    for (const [key, value] of [
      ['donation_q', q],
      ['donation_view', view],
      ['donation_status', status],
      ['donation_expanded', expanded],
    ]) {
      if (params.getAll(key).length > 1 || (params.has(key) && params.get(key) !== value)) {
        next.delete(key);
        if (value) next.set(key, value);
      }
    }
    if (next.toString() !== params.toString()) setParams(next, { replace: true });
  }, [params, setParams, q, status, expanded, view]);
  const family = [...economyKeys.donations, 'pages', accountID, view, status, q];
  const query = useQuery({
    queryKey: [...family, pager.page, pager.pageSize],
    queryFn: ({ signal }) =>
      economySessionRequest(
        client,
        () =>
          getOwnerDonationsPage(
            { view, review_status: status, q },
            pager.page,
            pager.pageSize,
            signal,
          ),
        accountID,
      ),
    enabled,
    retry: false,
    placeholderData: (previous, previousQuery) =>
      inFamily(previousQuery?.queryKey, family) ? previous : undefined,
  });
  const setFilter = (key: string, value: string) =>
    setParams((previous) => {
      const next = new URLSearchParams(previous);
      next.delete(key);
      if (value) next.set(key, value);
      next.delete('donations_page');
      return next;
    });
  const search = () => {
    if (!searchValid(shownDraft.value)) {
      setDraft({ ...shownDraft, invalid: true });
      return;
    }
    setDraft({ source: shownDraft.value, value: shownDraft.value, invalid: false });
    setFilter('donation_q', shownDraft.value);
  };
  const select = (id: string) =>
    setParams((previous) => {
      const next = new URLSearchParams(previous);
      next.delete('donation_expanded');
      next.delete('donation_keys_page');
      next.delete('donation_keys_view');
      if (id) next.set('donation_expanded', id);
      return next;
    });
  return (
    <div className="economy-owner-pages">
      <Tabs
        label={t('user.charity.ownerHistory.views')}
        value={view}
        tabs={VIEWS.map((value) => ({ value, label: t(viewLabel[value]) }))}
        onChange={(value) =>
          setParams((previous) => {
            const next = new URLSearchParams(previous);
            next.set('donation_view', value);
            for (const key of [
              'donation_status',
              'donations_page',
              'donation_expanded',
              'donation_keys_view',
            ])
              next.delete(key);
            return next;
          })
        }
      />
      <FilterBar
        secondaryLabel={t('user.charity.ownerPages.status')}
        activeCount={status ? 1 : 0}
        secondaryAlwaysVisible
        onSubmit={search}
        search={
          <>
            <Field
              label={t('user.charity.ownerPages.search')}
              error={shownDraft.invalid ? t('user.charity.ownerPages.invalidSearch') : undefined}
            >
              {(props) => (
                <input
                  {...props}
                  type="search"
                  placeholder={t('user.charity.ownerPages.searchPlaceholder')}
                  maxLength={256}
                  value={shownDraft.value}
                  onChange={(event) =>
                    setDraft({ source: q, value: event.target.value, invalid: false })
                  }
                />
              )}
            </Field>
            <div className="form-actions">
              <Button type="submit">{t('common.search')}</Button>
              <Button
                type="button"
                variant="ghost"
                onClick={() => {
                  setDraft({ source: '', value: '', invalid: false });
                  setFilter('donation_q', '');
                }}
              >
                {t('user.charity.ownerPages.clear')}
              </Button>
            </div>
          </>
        }
        secondary={
          <Field label={t('user.charity.ownerPages.status')}>
            {(props) => (
              <select
                {...props}
                value={status}
                onChange={(event) => setFilter('donation_status', event.target.value)}
              >
                {statuses.map((value) => (
                  <option key={value} value={value}>
                    {value ? t('common.connections.donationStates.' + value) : t('common.all')}
                  </option>
                ))}
              </select>
            )}
          </Field>
        }
      />
      {view === 'history' || view === 'hidden' ? (
        <DonationHistoryBatch
          accountID={accountID}
          scope={{
            action: view === 'hidden' ? 'restore' : 'hide',
            q,
            status:
              status === 'deleted' || status === 'expired' || status === 'rejected' ? status : null,
          }}
          disabled={
            !enabled || query.isFetching || Boolean(query.error) || !query.data?.data.length
          }
        />
      ) : null}
      {query.error ? <ErrorState error={query.error} onRetry={() => void query.refetch()} /> : null}
      {query.isPending ? <LoadingState /> : null}
      {query.data && !accessLost(query.error) ? (
        <>
          <PagePagination
            alwaysShowJump
            metadata={query.data.pagination}
            requestedPage={pager.page}
            onPageChange={pager.setPage}
            onPageSizeChange={pager.setPageSize}
            busy={query.isFetching}
          />
          {!query.data.data.length ? (
            <EmptyState
              title={t('user.charity.noDonations')}
              body={t('user.charity.ownerPages.noMatches')}
            />
          ) : null}
          <div className="economy-donation-list nb-list" aria-busy={query.isFetching}>
            {query.data.data.map((item) => (
              <DonationSummary
                key={item.id}
                item={item}
                disabled={query.isFetching || Boolean(query.error)}
                returnTo={`${location.pathname}${location.search}`}
                onOpenKeys={() => select(item.id)}
              />
            ))}
          </div>
        </>
      ) : null}
      {expanded && !accessLost(query.error) ? (
        <Panel aria-label={t('user.charity.ownerPages.keys')}>
          <PanelHead
            level={3}
            title={t('user.charity.donationNumber', { id: expanded })}
            actions={
              <Button type="button" variant="ghost" onClick={() => select('')}>
                {t('common.close')}
              </Button>
            }
          />
          <PanelBody>
            {query.data?.data.find((item) => item.id === expanded)?.useSummary ? (
              <DonationUsesPanel role="owner" accountId={accountID} donationId={expanded} />
            ) : null}
            <OwnerDonationKeys
              key={`${accountID}:${expanded}`}
              accountID={accountID}
              donationID={expanded}
              enabled={enabled && !query.isPending}
              summary={query.data?.data.find((item) => item.id === expanded)?.useSummary}
              status={
                query.data?.data.find((item) => item.id === expanded)?.status ??
                (view === 'history' || view === 'hidden' ? 'deleted' : undefined)
              }
            />
          </PanelBody>
        </Panel>
      ) : null}
    </div>
  );
}

function DonationSummary({
  item,
  disabled,
  returnTo,
  onOpenKeys,
}: {
  item: OwnerDonationSummary;
  disabled: boolean;
  returnTo: string;
  onOpenKeys: () => void;
}) {
  const { t } = useTranslation();
  return (
    <article className="nb-row economy-donation-summary">
      <div className="nb-row__main">
        <div className="nb-row__title">
          <h3>{t('user.charity.donationNumber', { id: item.id })}</h3>
          <DonationStatusBadge status={item.status} summary={item.useSummary} />
        </div>
        <p className="nb-row__sub" title={item.description}>
          {item.description}
        </p>
        <div className="nb-row__facts">
          <span>
            {t('user.charity.presentation.keyFacts', {
              total: item.keyCount,
              available: item.stateCounts.available,
            })}
          </span>
          <UseSummary value={item.useSummary} />
          <DonationThanks value={item.discordPublicThanks} />
          <p className="item-meta">{formatDateTime(item.createdAt)}</p>
        </div>
        <Fold plain title={t('user.charity.presentation.sourceDetails')}>
          <div className="economy-status-stack">
            {Object.entries(item.stateCounts)
              .filter(([, count]) => count !== '0')
              .map(([state, count]) => (
                <span key={state}>
                  {t(`user.charity.keyState.${state}`)} · <ExactCount value={count} />
                </span>
              ))}
          </div>
          <ul className="ops-source-preview">
            {item.sources.map((source) => (
              <li
                key={
                  source.kind === 'mainstream'
                    ? `channel:${source.channelId}`
                    : `${source.connectorType}:${source.baseUrl}`
                }
              >
                {source.kind === 'mainstream' ? source.name : t('user.charity.customSource')} ·{' '}
                {source.baseUrl} · <DonationConnectorLabel value={source.connectorType} />
              </li>
            ))}
          </ul>
          {BigInt(item.sourceCount) > BigInt(item.sources.length) ? (
            <p className="muted">
              {t('user.charity.ownerPages.sourcePreview', {
                shown: item.sources.length,
                total: item.sourceCount,
              })}
            </p>
          ) : null}
        </Fold>
      </div>
      <div className="donation-summary-actions">
        {terminal(item.status) ? (
          <DonationHistoryToggle
            id={item.id}
            hidden={item.ownerHiddenAt !== null}
            disabled={disabled}
          />
        ) : null}
        {disabled ? (
          <span className="nb-btn nb-btn--ghost" aria-disabled="true">
            {t('user.charity.openDonationDetail')}
          </span>
        ) : (
          <Link
            className="nb-btn nb-btn--ghost"
            to={`/charity/donations/${item.id}`}
            state={{ returnTo }}
          >
            {t('user.charity.openDonationDetail')}
          </Link>
        )}
        <MoreMenu
          label={t('user.charity.presentation.donationActions')}
          items={[{ label: t('user.charity.ownerPages.keys'), disabled, onSelect: onOpenKeys }]}
        />
      </div>
    </article>
  );
}

export function OwnerDonationKeys({
  accountID,
  donationID,
  status,
  summary,
  enabled = true,
}: {
  accountID: string;
  donationID: string;
  status?: DonationStatus;
  summary?: DonationUseSummary;
  enabled?: boolean;
}) {
  const { t, i18n } = useTranslation();
  const copy = copyForRecurringLimits(i18n.language);
  const location = useLocation();
  const [params, setParams] = useSearchState();
  const rawView = single(params, 'donation_keys_view');
  const view: OwnerDonationKeyView = ['current', 'ended', 'all'].includes(rawView)
    ? (rawView as OwnerDonationKeyView)
    : terminal(status)
      ? 'ended'
      : 'current';
  const returnTo =
    location.pathname === '/charity'
      ? `${location.pathname}${location.search}`
      : listReturnPath(location.state, '/charity');
  const client = useQueryClient();
  const pager = useUrlPagePager({
    station: 'user',
    listType: 'owner-donation-keys',
    scopeKey: `${accountID}:${donationID}`,
    pageParam: 'donation_keys_page',
    pageSizeParam: 'donation_keys_page_size',
  });
  const family = [...economyKeys.donations, 'keys-page', accountID, donationID, view];
  const query = useQuery({
    queryKey: [...family, pager.page, pager.pageSize],
    queryFn: ({ signal }) =>
      economySessionRequest(
        client,
        () => getOwnerDonationKeysPage(donationID, pager.page, pager.pageSize, signal, view),
        accountID,
      ),
    enabled,
    retry: false,
    placeholderData: (previous, previousQuery) =>
      inFamily(previousQuery?.queryKey, family) ? previous : undefined,
  });
  useRateLimitRefresh(
    enabled
      ? (query.data?.data
          .filter((key) => key.charityState === 'rate_limited')
          .flatMap((key) => [key.rateLimitedUntil, smartBackoffDeadline(key.smartBackoff)]) ?? [])
      : [],
    () => query.refetch({ throwOnError: true }),
  );
  return (
    <div className="economy-owner-keys">
      <Tabs
        label={t('user.charity.ownerHistory.keyViews')}
        value={view}
        tabs={(['current', 'ended', 'all'] as const).map((value) => ({
          value,
          label: t(keyViewLabel[value]),
        }))}
        onChange={(value) =>
          setParams((previous) => {
            const next = new URLSearchParams(previous);
            next.set('donation_keys_view', value);
            next.delete('donation_keys_page');
            return next;
          })
        }
      />
      {query.error ? <ErrorState error={query.error} onRetry={() => void query.refetch()} /> : null}
      {query.isPending ? <LoadingState /> : null}
      {query.data && !accessLost(query.error) ? (
        <>
          <PagePagination
            alwaysShowJump
            metadata={query.data.pagination}
            requestedPage={pager.page}
            onPageChange={pager.setPage}
            onPageSizeChange={pager.setPageSize}
            busy={query.isFetching}
          />
          {!query.data.data.length ? <p>{t('user.charity.noRemainingKeys')}</p> : null}
          <fieldset
            className="ops-unframed"
            disabled={query.isFetching || Boolean(query.error)}
            aria-busy={query.isFetching}
          >
            {query.data.data.map((key) => (
              <DonationKeyPanel
                key={key.id}
                donationKey={key}
                donationId={donationID}
                donationStatus={status}
                donationSummary={summary}
                donationRevision={key.donationRevision}
                accountID={accountID}
                returnTo={returnTo}
                ruleSummary={
                  key.ruleCount !== '0' ? (
                    <div className="economy-owner-rule-preview">
                      <strong>{copy.ruleCount(Number(key.ruleCount))}</strong>
                      {key.rules.map((rule) => (
                        <p key={rule.id}>
                          {copy.modeValue[rule.mode]} · {copy.intervalValue[rule.interval]} ·{' '}
                          {copy.metricValue[rule.metric]} · {copy.state[rule.state]}
                          <br />
                          {copy.limit}: {rule.limit} · {copy.used}: {rule.used} · {copy.reserved}:{' '}
                          {rule.reserved} · {copy.remaining}: {rule.remaining}
                        </p>
                      ))}
                      {BigInt(key.ruleCount) > BigInt(key.rules.length) ? (
                        <p className="muted">
                          {t('user.charity.ownerPages.rulesPreview', {
                            shown: key.rules.length,
                            total: key.ruleCount,
                          })}
                        </p>
                      ) : null}
                    </div>
                  ) : null
                }
                compact
              />
            ))}
          </fieldset>
        </>
      ) : null}
    </div>
  );
}
