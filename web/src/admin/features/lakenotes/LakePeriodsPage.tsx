import { useState } from 'react';
import { Link } from 'react-router';
import { useQuery } from '@tanstack/react-query';
import { Card, ErrorState, LoadingState, PageHeader } from '@shared/components/States';
import { getPeriods, unitsToNatural } from '@shared/lakenotes/api';
import { useDateTimeFormatter } from '@shared/utils/datetime';
import { useAdminSession } from '../../data';
import { useLakeAdminCopy } from './copy';
import { useGameAdminText } from '../games/copy';
import './periods.css';

export function LakePeriodsPage() {
  const text = useGameAdminText(),
    { t: lakeText } = useLakeAdminCopy(),
    format = useDateTimeFormatter();
  const session = useAdminSession(),
    [page, setPage] = useState(1);
  const periods = useQuery({
    queryKey: ['admin', 'lake-notes', 'periods', page],
    queryFn: ({ signal }) => getPeriods(page, { signal }),
    enabled: Boolean(session.data?.admin && !session.error && !session.isFetching),
  });
  return (
    <div className="page">
      <PageHeader
        title={text('垂钓手记 · 历史期次', 'Lake Notes · past periods')}
        icon="games"
        description={lakeText('periodHistoryDescription')}
        back={<Link to="/games">{text('返回游戏配置', 'Back to game settings')}</Link>}
      />
      {session.error || periods.error ? (
        <ErrorState error={session.error ?? periods.error} />
      ) : periods.isPending ? (
        <LoadingState />
      ) : (
        <Card>
          {!periods.data.items.length ? (
            <p>{lakeText('empty')}</p>
          ) : (
            <div className="lake-period-list">
              {periods.data.items.map((p) => (
                <article key={p.id}>
                  <h3>{p.name}</h3>
                  <p>
                    {lakeText(p.status)} · {format(p.starts_at)} — {format(p.ends_at)}
                  </p>
                  <p>
                    {lakeText('fee')}:{' '}
                    {p.entry_fee_milli === null
                      ? '—'
                      : unitsToNatural(p.entry_fee_milli, 'general')}
                  </p>
                </article>
              ))}
            </div>
          )}
          <div className="lake-period-actions">
            <button
              className="btn btn-secondary"
              disabled={page === 1}
              onClick={() => setPage((p) => p - 1)}
            >
              {lakeText('previous')}
            </button>
            <span>{page}</span>
            <button
              className="btn btn-secondary"
              disabled={!periods.data.has_more}
              onClick={() => setPage((p) => p + 1)}
            >
              {lakeText('next')}
            </button>
          </div>
        </Card>
      )}
    </div>
  );
}
