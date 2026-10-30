import { CreditChange } from '../features/credits/CreditChange';
import { Field, FilterBar, Segmented } from '@shared/components/ui';
import { RecordsHeader } from '../components/RecordsHeader';
import { getWallet } from '@shared/limitedactivities/api';
import { limitedActivityKeys } from '../features/limitedactivities/queries';
import { economySessionRequest } from '../features/economy/queries';
import { useEffect, useState } from 'react';
import { CancelledError, keepPreviousData, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link } from 'react-router';
import { Card, EmptyState, ErrorState, LoadingState } from '@shared/components/States';
import { TimeInput } from '@shared/components/TimeInput';
import { PagePagination } from '@shared/operations/PagePagination';
import { createTimeDraft, timeDraftValue, type TimeDraft } from '@shared/time';
import { formatDateTime } from '@shared/utils/datetime';
import { UserPageGate } from '../components/UserPageGate';
import { useUserSession } from '../data';
import { coreSessionMatchesAccount } from '../features/core/queries';
import { ExactCredits, ExactCount } from '../features/core/components';
import {
  HISTORY_ASSET_FILTERS,
  HISTORY_CATEGORIES,
  MAX_HISTORY_PAGE,
  loadHistory,
  type HistoryPage,
} from '../features/credits/data';
import { useCreditCopy } from '../features/credits/copy';
import { useCreditHistoryUrl } from '../features/credits/url';
import '../features/credits/credits.css';

function CreditHistory({
  accountID,
  scopeReset = false,
}: {
  accountID: string;
  scopeReset?: boolean;
}) {
  const { copy, reason } = useCreditCopy();
  const client = useQueryClient();
  const url = useCreditHistoryUrl(scopeReset);
  const [fromTimeDraft, setFromTimeDraft] = useState<TimeDraft>(() =>
    createTimeDraft(url.filter.from ?? null),
  );
  const [toTimeDraft, setToTimeDraft] = useState<TimeDraft>(() =>
    createTimeDraft(url.filter.to ?? null),
  );
  const [validation, setValidation] = useState<'range' | null>(null);
  useEffect(() => {
    let active = true;
    queueMicrotask(() => {
      if (!active) return;
      setFromTimeDraft(createTimeDraft(url.filter.from ?? null));
      setToTimeDraft(createTimeDraft(url.filter.to ?? null));
    });
    return () => {
      active = false;
    };
  }, [url.filter.from, url.filter.to]);
  const history = useQuery<HistoryPage, Error>({
    queryKey: ['user', 'credit-history', accountID, url.filter, url.refreshRevision],
    queryFn: async ({ signal }) => {
      if (!coreSessionMatchesAccount(client, accountID)) throw new CancelledError();
      const page = await loadHistory(
        { ...url.filter, asset_type: url.filter.asset_type ?? 'all' },
        signal,
      );
      if (!coreSessionMatchesAccount(client, accountID)) throw new CancelledError();
      return page;
    },
    placeholderData: keepPreviousData,
    retry: false,
  });
  const wallet = useQuery({
    queryKey: limitedActivityKeys.wallet(accountID),
    queryFn: () => economySessionRequest(client, getWallet, accountID),
    enabled: Boolean(accountID),
    retry: false,
  });
  const data = history.data;
  const busy = history.isFetching;
  const timeReady =
    timeDraftValue(fromTimeDraft) !== undefined && timeDraftValue(toTimeDraft) !== undefined;
  const reset = () => {
    setValidation(null);
    setFromTimeDraft(createTimeDraft(null));
    setToTimeDraft(createTimeDraft(null));
    url.reset();
  };
  const apply = () => {
    const fromValue = timeDraftValue(fromTimeDraft);
    const toValue = timeDraftValue(toTimeDraft);
    if (fromValue === undefined || toValue === undefined) {
      setValidation('range');
      return;
    }
    const from = fromValue === null ? undefined : fromValue;
    const to = toValue === null ? undefined : toValue;
    if (
      [from, to].some(
        (value) =>
          value !== undefined &&
          (!Number.isSafeInteger(value) || value < 0 || value > 253_402_300_799),
      ) ||
      (from !== undefined && to !== undefined && from >= to)
    ) {
      setValidation('range');
      return;
    }
    setValidation(null);
    url.apply({
      asset_type: url.filter.asset_type ?? 'all',
      category: url.filter.category,
      direction: url.filter.direction,
      from,
      to,
    });
  };
  return (
    <div className="page credit-history">
      <RecordsHeader
        description={copy.description}
        actions={
          <button
            type="button"
            className="nb-btn nb-btn--secondary"
            disabled={busy}
            onClick={url.refresh}
          >
            {copy.refresh}
          </button>
        }
      />
      <Card>
        <div className="nb-stats credit-history__overview" aria-label={copy.asset}>
          {[
            [copy.balance, history.isError ? '—' : (data?.current_balance ?? '—'), 'credits'],
            [copy.game, history.isError ? '—' : (data?.game_balance ?? '—'), 'credits'],
            [copy.sketch_paper, wallet.isError ? '—' : (wallet.data?.sketch_paper ?? '—'), 'count'],
            [copy.sketch_brush, wallet.isError ? '—' : (wallet.data?.sketch_brush ?? '—'), 'count'],
          ].map(([label, value, type]) => (
            <div
              className={`nb-stat${value.length > 12 ? ' credit-history__balance--long' : ''}`}
              key={label}
            >
              <span>{label}</span>
              <strong>
                {value === '—' ? (
                  value
                ) : type === 'count' ? (
                  <ExactCount value={value} />
                ) : (
                  <ExactCredits value={value} />
                )}
              </strong>
            </div>
          ))}
        </div>
        {wallet.isError ? (
          <ErrorState error={wallet.error} onRetry={() => void wallet.refetch()} />
        ) : null}
        <p className="credit-history__time-note">{copy.note}</p>
        <Segmented
          label={copy.asset}
          value={url.filter.asset_type ?? 'all'}
          options={['all', ...HISTORY_ASSET_FILTERS.filter((asset) => asset !== 'all')].map(
            (value) => ({
              value: value as (typeof HISTORY_ASSET_FILTERS)[number],
              label: copy[value as (typeof HISTORY_ASSET_FILTERS)[number]],
            }),
          )}
          onChange={(asset_type) => url.apply({ ...url.filter, asset_type })}
        />
        <FilterBar
          onSubmit={apply}
          onReset={reset}
          submitLabel={copy.apply}
          submitDisabled={busy || !timeReady}
          activeCount={[url.filter.from, url.filter.to].filter(Boolean).length}
          search={
            <>
              <Field label={copy.category} size="short">
                {(props) => (
                  <select
                    {...props}
                    value={url.filter.category ?? ''}
                    onChange={(event) =>
                      url.apply({ ...url.filter, category: event.target.value || undefined })
                    }
                  >
                    <option value="">{copy.all}</option>
                    {HISTORY_CATEGORIES.map((key) => (
                      <option key={key} value={key}>
                        {copy[key]}
                      </option>
                    ))}
                  </select>
                )}
              </Field>
              <Field label={copy.direction} size="short">
                {(props) => (
                  <select
                    {...props}
                    value={url.filter.direction ?? ''}
                    onChange={(event) =>
                      url.apply({ ...url.filter, direction: event.target.value || undefined })
                    }
                  >
                    <option value="">{copy.all}</option>
                    <option value="income">{copy.income}</option>
                    <option value="expense">{copy.expense}</option>
                  </select>
                )}
              </Field>
            </>
          }
          secondary={
            <>
              <TimeInput
                station="user"
                label={copy.from}
                showZoneHint={false}
                draft={fromTimeDraft}
                onChange={setFromTimeDraft}
              />
              <TimeInput
                station="user"
                label={copy.to}
                showZoneHint={false}
                draft={toTimeDraft}
                onChange={setToTimeDraft}
              />
            </>
          }
        />
        <p className="credit-history__time-note">{copy.localTime}</p>
        {validation ? (
          <p className="field-error" role="alert">
            {copy.invalidRange}
          </p>
        ) : null}
        <div className="credit-history__results" aria-busy={busy}>
          {history.isPending ? (
            <LoadingState />
          ) : history.error ? (
            <ErrorState error={history.error} onRetry={() => void history.refetch()} />
          ) : data ? (
            <>
              {data.data.length === 0 ? (
                <EmptyState title={copy.empty} body={copy.emptyBody} />
              ) : (
                <div className="credit-history__table-wrap">
                  <table className="credit-history__table">
                    <thead>
                      <tr>
                        <th>{copy.time}</th>
                        <th>{copy.asset}</th>
                        <th>{copy.change}</th>
                        <th>{copy.category}</th>
                        {data.data.some((entry) => entry.request_id !== null) ? (
                          <th>{copy.request}</th>
                        ) : null}
                      </tr>
                    </thead>
                    <tbody>
                      {data.data.map((entry) => (
                        <tr key={`${entry.operation_id}:${entry.line}`}>
                          <td data-label={copy.time}>
                            <time dateTime={new Date(entry.created_at * 1000).toISOString()}>
                              {formatDateTime(entry.created_at)}
                            </time>
                          </td>
                          <td data-label={copy.asset}>{copy[entry.asset_type]}</td>
                          <td data-label={copy.change} className="credit-history__amount">
                            <CreditChange value={entry.delta} />
                          </td>
                          <td data-label={copy.category}>{reason(entry)}</td>
                          {data.data.some((row) => row.request_id !== null) ? (
                            <td data-label={copy.request}>
                              {entry.request_id ? (
                                <Link
                                  to={`/logs?request_id=${encodeURIComponent(entry.request_id)}`}
                                >
                                  {copy.openRequest}
                                </Link>
                              ) : (
                                copy.noRequest
                              )}
                            </td>
                          ) : null}
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
              <div className="credit-history__pagination">
                <PagePagination
                  metadata={{
                    page: data.page,
                    page_size: data.page_size,
                    total_items: data.total,
                    total_pages: data.total_pages,
                  }}
                  requestedPage={url.filter.page}
                  maxPage={MAX_HISTORY_PAGE}
                  busy={busy}
                  onPageChange={(page) => url.setPage(page, data.anchor)}
                  onPageSizeChange={(pageSize) => url.setPageSize(pageSize, data.anchor)}
                />
              </div>
            </>
          ) : null}
        </div>
      </Card>
    </div>
  );
}

export function CreditsPage() {
  const session = useUserSession();
  const [knownAccount, setKnownAccount] = useState<string | undefined>(undefined);
  const accountID = session.data?.user.id;
  const scopeReset =
    accountID !== undefined && knownAccount !== undefined && knownAccount !== accountID;
  useEffect(() => {
    if (accountID === undefined || knownAccount === accountID) return;
    let active = true;
    queueMicrotask(() => {
      if (active) setKnownAccount(accountID);
    });
    return () => {
      active = false;
    };
  }, [accountID, knownAccount]);
  return (
    <UserPageGate>
      {accountID ? (
        <CreditHistory key={accountID} accountID={accountID} scopeReset={scopeReset} />
      ) : null}
    </UserPageGate>
  );
}
