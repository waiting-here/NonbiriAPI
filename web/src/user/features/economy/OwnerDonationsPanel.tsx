import { useEffect, useState, type FormEvent } from 'react';
import { useQuery, useQueryClient, type QueryKey } from '@tanstack/react-query';
import { Link, useLocation } from 'react-router';
import { useSearchState } from '@shared/operations/useSearchState';
import { useTranslation } from 'react-i18next';
import { Card, EmptyState, ErrorState, LoadingState, StatusBadge } from '@shared/components/States';
import { PagePagination } from '@shared/operations/PagePagination';
import { useUrlPagePager } from '@shared/operations/useUrlPagePager';
import { formatDateTime } from '@shared/utils/datetime';
import { isForbidden, isUnauthorized } from '@shared/query/http';
import { isStationSessionChanged } from '@shared/charityManagement';
import { listReturnPath } from '@shared/operations/listReturn';
import { copyForRecurringLimits } from '@shared/components/recurringLimitsCopy';
import { DonationKeyPanel } from './CharityPanels';
import { DonationThanks } from '@shared/components/DonationControlFacts';
import { economyKeys, economySessionRequest } from './queries';
import {
  getOwnerDonationsPage,
  getOwnerDonationKeysPage,
  type OwnerDonationSummary,
} from './donationPages';
import { ExactCount } from './ExactValue';
import type { DonationStatus } from './types';

const STATUSES = ['', 'pending', 'approved', 'rejected', 'deleted', 'expired'] as const;
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
  const rawStatus = single(params, 'donation_status');
  const status = STATUSES.includes(rawStatus as DonationStatus)
    ? (rawStatus as DonationStatus | '')
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
      ['donation_status', status],
      ['donation_expanded', expanded],
    ]) {
      if (params.getAll(key).length > 1 || (params.has(key) && params.get(key) !== value)) {
        next.delete(key);
        if (value) next.set(key, value);
      }
    }
    if (next.toString() !== params.toString()) setParams(next, { replace: true });
  }, [params, setParams, q, status, expanded]);
  const family = [...economyKeys.donations, 'pages', accountID, status, q];
  const query = useQuery({
    queryKey: [...family, pager.page, pager.pageSize],
    queryFn: ({ signal }) =>
      economySessionRequest(
        client,
        () => getOwnerDonationsPage({ status, q }, pager.page, pager.pageSize, signal),
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
  const search = (event: FormEvent) => {
    event.preventDefault();
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
      if (id) next.set('donation_expanded', id);
      return next;
    });
  return (
    <div className="economy-owner-pages">
      <form className="economy-owner-filters" onSubmit={search}>
        <label>
          <span>{t('user.charity.ownerPages.search')}</span>
          <input
            type="search"
            maxLength={256}
            value={shownDraft.value}
            aria-invalid={shownDraft.invalid}
            onChange={(event) => setDraft({ source: q, value: event.target.value, invalid: false })}
          />
        </label>
        <label>
          <span>{t('user.charity.ownerPages.status')}</span>
          <select
            value={status}
            onChange={(event) => setFilter('donation_status', event.target.value)}
          >
            {STATUSES.map((value) => (
              <option key={value} value={value}>
                {value ? t(`user.charity.status.${value}`) : t('common.all')}
              </option>
            ))}
          </select>
        </label>
        <button type="submit" className="btn btn-secondary">
          {t('common.search')}
        </button>
        <button
          type="button"
          className="btn btn-quiet"
          onClick={() => {
            setDraft({ source: '', value: '', invalid: false });
            setFilter('donation_q', '');
          }}
        >
          {t('user.charity.ownerPages.clear')}
        </button>
      </form>
      {shownDraft.invalid ? (
        <p className="field-error" role="alert">
          {t('user.charity.ownerPages.invalidSearch')}
        </p>
      ) : null}
      {query.error ? <ErrorState error={query.error} onRetry={() => void query.refetch()} /> : null}
      {query.isPending ? <LoadingState /> : null}
      {query.data && !accessLost(query.error) ? (
        <>
          <PagePagination
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
          <div className="economy-donation-list" aria-busy={query.isFetching}>
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
        <section className="ops-subcard" aria-label={t('user.charity.ownerPages.keys')}>
          <div className="item-header">
            <h3>{t('user.charity.donationNumber', { id: expanded })}</h3>
            <button type="button" className="btn btn-quiet" onClick={() => select('')}>
              {t('common.close')}
            </button>
          </div>
          <OwnerDonationKeys
            key={`${accountID}:${expanded}`}
            accountID={accountID}
            donationID={expanded}
            enabled={enabled}
          />
        </section>
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
    <Card className="economy-donation-card">
      <div className="item-header">
        <h3>{t('user.charity.donationNumber', { id: item.id })}</h3>
        <StatusBadge
          active={item.status === 'approved'}
          danger={['rejected', 'deleted', 'expired'].includes(item.status)}
          label={t(`user.charity.status.${item.status}`)}
        />
      </div>
      <p>{item.description}</p>
      <DonationThanks value={item.discordPublicThanks} />
      <p className="item-meta">{formatDateTime(item.createdAt)}</p>
      <p>{t('user.charity.ownerPages.keyCount', { count: item.keyCount })}</p>
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
            {source.baseUrl} · {source.connectorType}
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
      <div className="form-actions">
        <button
          type="button"
          className="btn btn-secondary"
          disabled={disabled}
          onClick={onOpenKeys}
        >
          {t('user.charity.ownerPages.keys')}
        </button>
        {disabled ? (
          <span className="btn btn-quiet" aria-disabled="true">
            {t('user.charity.openDonationDetail')}
          </span>
        ) : (
          <Link className="btn btn-quiet" to={`/charity/donations/${item.id}`} state={{ returnTo }}>
            {t('user.charity.openDonationDetail')}
          </Link>
        )}
      </div>
    </Card>
  );
}

export function OwnerDonationKeys({
  accountID,
  donationID,
  enabled = true,
}: {
  accountID: string;
  donationID: string;
  enabled?: boolean;
}) {
  const { t, i18n } = useTranslation();
  const copy = copyForRecurringLimits(i18n.language);
  const location = useLocation();
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
  const family = [...economyKeys.donations, 'keys-page', accountID, donationID];
  const query = useQuery({
    queryKey: [...family, pager.page, pager.pageSize],
    queryFn: ({ signal }) =>
      economySessionRequest(
        client,
        () => getOwnerDonationKeysPage(donationID, pager.page, pager.pageSize, signal),
        accountID,
      ),
    enabled,
    retry: false,
    placeholderData: (previous, previousQuery) =>
      inFamily(previousQuery?.queryKey, family) ? previous : undefined,
  });
  return (
    <div>
      {query.error ? <ErrorState error={query.error} onRetry={() => void query.refetch()} /> : null}
      {query.isPending ? <LoadingState /> : null}
      {query.data && !accessLost(query.error) ? (
        <>
          <PagePagination
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
