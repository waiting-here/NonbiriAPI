import { useEffect, useState, type FormEvent } from 'react';
import { CancelledError, keepPreviousData, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link } from 'react-router';
import { Card, EmptyState, ErrorState, LoadingState, PageHeader } from '@shared/components/States';
import { TimeInput } from '@shared/components/TimeInput';
import { PagePagination } from '@shared/operations/PagePagination';
import { createTimeDraft, timeDraftValue, type TimeDraft } from '@shared/time';
import { formatDateTime } from '@shared/utils/datetime';
import { UserPageGate } from '../components/UserPageGate';
import { useUserSession } from '../data';
import { coreSessionMatchesAccount } from '../features/core/queries';
import {
  HISTORY_ASSET_FILTERS,
  HISTORY_CATEGORIES,
  MAX_HISTORY_PAGE,
  loadHistory,
  type HistoryPage,
  type HistoryFilter,
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
  const [draft, setDraft] = useState({
    asset_type: url.filter.asset_type ?? 'all',
    category: url.filter.category ?? '',
    direction: url.filter.direction ?? '',
  });
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
      setDraft({
        asset_type: url.filter.asset_type ?? 'all',
        category: url.filter.category ?? '',
        direction: url.filter.direction ?? '',
      });
      setFromTimeDraft(createTimeDraft(url.filter.from ?? null));
      setToTimeDraft(createTimeDraft(url.filter.to ?? null));
    });
    return () => {
      active = false;
    };
  }, [
    url.filter.asset_type,
    url.filter.category,
    url.filter.direction,
    url.filter.from,
    url.filter.to,
  ]);
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
  const data = history.data;
  const busy = history.isFetching;
  const fromValue = timeDraftValue(fromTimeDraft);
  const toValue = timeDraftValue(toTimeDraft);
  const timeReady = fromValue !== undefined && toValue !== undefined;
  const reset = () => {
    setValidation(null);
    url.reset();
  };
  const apply = (event: FormEvent) => {
    event.preventDefault();
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
      asset_type: draft.asset_type as HistoryFilter['asset_type'],
      category: draft.category || undefined,
      direction: draft.direction || undefined,
      from,
      to,
    });
  };
  return (
    <div className="page credit-history">
      <PageHeader
        title={copy.title}
        description={copy.description}
        actions={
          <button type="button" className="btn btn-secondary" disabled={busy} onClick={url.refresh}>
            {copy.refresh}
          </button>
        }
      />
      <Card>
        <div className="credit-history__overview">
          <span>{copy.balance}</span>
          <strong>{history.error ? '—' : (data?.current_balance ?? '—')}</strong>
          <span>{copy.game}</span>
          <strong>{history.error ? '—' : (data?.game_balance ?? '—')}</strong>
          <small>{copy.note}</small>
        </div>
        <form className="credit-history__filters" onSubmit={apply}>
          <label>
            {copy.asset}
            <select
              value={draft.asset_type}
              onChange={(e) =>
                setDraft({
                  ...draft,
                  asset_type: e.target.value as NonNullable<HistoryFilter['asset_type']>,
                })
              }
            >
              {HISTORY_ASSET_FILTERS.map((asset) => (
                <option key={asset} value={asset}>
                  {copy[asset]}
                </option>
              ))}
            </select>
          </label>
          <label>
            {copy.category}
            <select
              value={draft.category}
              onChange={(e) => setDraft({ ...draft, category: e.target.value })}
            >
              <option value="">{copy.all}</option>
              {HISTORY_CATEGORIES.map((key) => (
                <option key={key} value={key}>
                  {copy[key]}
                </option>
              ))}
            </select>
          </label>
          <label>
            {copy.direction}
            <select
              value={draft.direction}
              onChange={(e) => setDraft({ ...draft, direction: e.target.value })}
            >
              <option value="">{copy.all}</option>
              <option value="income">{copy.income}</option>
              <option value="expense">{copy.expense}</option>
            </select>
          </label>
          <TimeInput
            station="user"
            label={copy.from}
            draft={fromTimeDraft}
            onChange={setFromTimeDraft}
          />
          <TimeInput
            station="user"
            label={copy.to}
            draft={toTimeDraft}
            onChange={setToTimeDraft}
            showZoneHint={false}
          />
          <div className="credit-history__filter-actions">
            <button className="btn btn-primary" disabled={busy || !timeReady}>
              {copy.apply}
            </button>
            <button className="btn btn-secondary" type="button" disabled={busy} onClick={reset}>
              {copy.reset}
            </button>
          </div>
        </form>
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
                        <th>{copy.request}</th>
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
                          <td
                            data-label={copy.change}
                            className={`credit-history__amount ${entry.delta.startsWith('-') ? 'is-expense' : 'is-income'}`}
                          >
                            {entry.delta.startsWith('-') ? entry.delta : `+${entry.delta}`}
                          </td>
                          <td data-label={copy.category}>{reason(entry)}</td>
                          <td data-label={copy.request}>
                            {entry.request_id ? (
                              <Link to={`/logs?request_id=${encodeURIComponent(entry.request_id)}`}>
                                {copy.openRequest}
                              </Link>
                            ) : (
                              copy.noRequest
                            )}
                          </td>
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
