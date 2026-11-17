import { SimplePager } from '@shared/operations/SimplePager';
import { useState } from 'react';
import { Link } from 'react-router';
import { useQuery } from '@tanstack/react-query';
import { CreditNumber } from '@shared/components/CreditNumber';
import { EmptyState, ErrorState, LoadingState, PageHeader } from '@shared/components/States';
import { Panel, PanelBody, StatusBadge } from '@shared/components/ui';
import { getPeriods, unitsToNatural } from '@shared/lakenotes/api';
import { useDateTimeFormatter } from '@shared/utils/datetime';
import { formatCredits } from '../../../user/games/common/strict';
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
        description={lakeText('periodHistoryDescription')}
        back={<Link to="/games">{lakeText('back')}</Link>}
      />
      {session.error || periods.error ? (
        <ErrorState error={session.error ?? periods.error} />
      ) : periods.isPending ? (
        <LoadingState />
      ) : (
        <Panel>
          <PanelBody>
            {!periods.data.items.length ? (
              <EmptyState title={lakeText('emptyTitle')} body={lakeText('emptyBody')} />
            ) : (
              <div className="lake-period-list">
                {periods.data.items.map((p) => (
                  <article key={p.id}>
                    <div className="lake-period-heading">
                      <h3>{p.name}</h3>
                      <StatusBadge
                        tone={
                          p.status === 'published'
                            ? 'ok'
                            : p.status === 'cancelled'
                              ? 'bad'
                              : 'neutral'
                        }
                      >
                        {lakeText(p.status)}
                      </StatusBadge>
                    </div>
                    <p className="nb-small nb-muted">
                      {format(p.starts_at)} — {format(p.ends_at)}
                    </p>
                    <p>
                      {lakeText('fee')}：
                      {p.entry_fee_milli === null ? (
                        '—'
                      ) : (
                        <>
                          <CreditNumber
                            value={formatCredits(unitsToNatural(p.entry_fee_milli, 'general'))}
                          />{' '}
                          {lakeText('general')}
                        </>
                      )}
                    </p>
                  </article>
                ))}
              </div>
            )}
            <SimplePager
              page={page}
              hasMore={periods.data.has_more}
              onPrev={() => setPage((p) => p - 1)}
              onNext={() => setPage((p) => p + 1)}
              labels={{ previous: lakeText('previous'), next: lakeText('next') }}
            />
          </PanelBody>
        </Panel>
      )}
    </div>
  );
}
