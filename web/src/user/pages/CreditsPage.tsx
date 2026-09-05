import { useState, type FormEvent } from 'react';
import { CancelledError, keepPreviousData, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link } from 'react-router';
import { Card, EmptyState, ErrorState, LoadingState, PageHeader } from '@shared/components/States';
import { formatDateTime } from '@shared/utils/datetime';
import { UserPageGate } from '../components/UserPageGate';
import { useUserSession } from '../data';
import { coreSessionMatchesAccount } from '../features/core/queries';
import { HISTORY_CATEGORIES, loadHistory, type HistoryFilter } from '../features/credits/data';
import { useCreditCopy } from '../features/credits/copy';
import '../features/credits/credits.css';

function CreditHistory({ accountID }: { accountID: string }) {
  const { copy, reason } = useCreditCopy();
  const client = useQueryClient();
  const [filter, setFilter] = useState<HistoryFilter>({ page: '1', page_size: 20 });
  const [draft, setDraft] = useState({ category: '', direction: '', from: '', to: '' });
  const [revision, setRevision] = useState(0);
  const [validation, setValidation] = useState<'range' | 'page' | null>(null);
  const [jump, setJump] = useState('');
  const history = useQuery({
    queryKey: ['user', 'credit-history', accountID, filter, revision],
    queryFn: async ({ signal }) => {
      if (!coreSessionMatchesAccount(client, accountID)) throw new CancelledError();
      const page = await loadHistory(filter, signal);
      if (!coreSessionMatchesAccount(client, accountID)) throw new CancelledError();
      return page;
    },
    placeholderData: keepPreviousData,
    retry: false,
  });
  const data = history.data;
  const busy = history.isFetching;
  const move = (page: string, pageSize = filter.page_size) => {
    setValidation(null);
    setJump('');
    setFilter({ ...filter, page, page_size: pageSize, anchor: data?.anchor ?? undefined });
  };
  const reset = () => {
    setValidation(null);
    setJump('');
    setFilter({ page: '1', page_size: filter.page_size });
    setDraft({ category: '', direction: '', from: '', to: '' });
    setRevision((value) => value + 1);
  };
  const apply = (event: FormEvent) => {
    event.preventDefault();
    const parse = (value: string) =>
      value ? Math.floor(new Date(value).getTime() / 1000) : undefined;
    const from = parse(draft.from),
      to = parse(draft.to);
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
    setJump('');
    setFilter({
      page: '1',
      page_size: filter.page_size,
      category: draft.category || undefined,
      direction: draft.direction || undefined,
      from,
      to,
    });
    setRevision((value) => value + 1);
  };
  const jumpTo = (event: FormEvent) => {
    event.preventDefault();
    if (!data || !/^[1-9][0-9]{0,18}$/.test(jump) || BigInt(jump) > BigInt(data.total_pages)) {
      setValidation('page');
      return;
    }
    move(jump);
  };
  return (
    <div className="page credit-history">
      <PageHeader
        title={copy.title}
        description={copy.description}
        actions={
          <button
            className="btn btn-secondary"
            disabled={busy}
            onClick={() => {
              setFilter({ ...filter, page: '1', anchor: undefined });
              setRevision((value) => value + 1);
            }}
          >
            {copy.refresh}
          </button>
        }
      />
      <Card>
        <div className="credit-history__overview">
          <span>{copy.balance}</span>
          <strong>{data?.current_balance ?? '—'}</strong>
          <small>{copy.note}</small>
        </div>
        <form className="credit-history__filters" onSubmit={apply}>
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
          <label>
            {copy.from}
            <input
              type="datetime-local"
              value={draft.from}
              onChange={(e) => setDraft({ ...draft, from: e.target.value })}
            />
          </label>
          <label>
            {copy.to}
            <input
              type="datetime-local"
              value={draft.to}
              onChange={(e) => setDraft({ ...draft, to: e.target.value })}
            />
          </label>
          <div className="credit-history__filter-actions">
            <button className="btn btn-primary" disabled={busy}>
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
            {validation === 'range' ? copy.invalidRange : copy.invalidPage}
          </p>
        ) : null}
        {history.isPending ? (
          <LoadingState />
        ) : history.error ? (
          <ErrorState error={history.error} onRetry={() => void history.refetch()} />
        ) : data ? (
          <>
            {data.data.length === 0 ? (
              <EmptyState title={copy.empty} body={copy.emptyBody} />
            ) : (
              <div className="credit-history__table-wrap" aria-busy={busy}>
                <table className="credit-history__table">
                  <thead>
                    <tr>
                      <th>{copy.time}</th>
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
              <span>
                {data.total} {copy.records}
              </span>
              <label>
                {copy.pageSize}
                <select
                  value={filter.page_size}
                  disabled={busy}
                  onChange={(e) => move('1', Number(e.target.value))}
                >
                  {[20, 50, 100].map((size) => (
                    <option key={size} value={size}>
                      {size}
                    </option>
                  ))}
                </select>
              </label>
              <nav aria-label={copy.title}>
                <button
                  className="btn btn-secondary"
                  disabled={busy || data.page === '1'}
                  onClick={() => move('1')}
                >
                  {copy.first}
                </button>
                <button
                  className="btn btn-secondary"
                  disabled={busy || data.page === '1'}
                  onClick={() => move((BigInt(data.page) - 1n).toString())}
                >
                  {copy.previous}
                </button>
                <span>
                  {copy.page} {data.page} {copy.of} {data.total_pages}
                </span>
                <button
                  className="btn btn-secondary"
                  disabled={busy || data.page === data.total_pages}
                  onClick={() => move((BigInt(data.page) + 1n).toString())}
                >
                  {copy.next}
                </button>
                <button
                  className="btn btn-secondary"
                  disabled={busy || data.page === data.total_pages}
                  onClick={() => move(data.total_pages)}
                >
                  {copy.last}
                </button>
              </nav>
              <form onSubmit={jumpTo}>
                <input
                  aria-label={copy.jumpLabel}
                  inputMode="numeric"
                  maxLength={19}
                  placeholder={data.page}
                  value={jump}
                  onChange={(e) => setJump(e.target.value)}
                />
                <button className="btn btn-secondary" disabled={busy}>
                  {copy.jump}
                </button>
              </form>
            </div>
          </>
        ) : null}
      </Card>
    </div>
  );
}

export function CreditsPage() {
  const session = useUserSession();
  return (
    <UserPageGate>
      {session.data ? (
        <CreditHistory key={session.data.user.id} accountID={session.data.user.id} />
      ) : null}
    </UserPageGate>
  );
}
