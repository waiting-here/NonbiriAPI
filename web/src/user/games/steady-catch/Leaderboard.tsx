import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { ErrorState, LoadingState } from '@shared/components/States';
import { Segmented } from '@shared/components/ui/Segmented';
import { gameRequest } from '../common/request';
import { PublicGameIdentity } from '../common/PublicGameIdentity';
import { GamePrivacyControl } from '../common/GamePrivacyControl';
import { publicIdentity } from '../common/strict';
import { formatCount } from '@shared/utils/formatNumber';
import { useCatchText } from './copy';
interface Row {
  rank: string;
  score: number;
  achieved_at: number;
  is_me: boolean;
  identity: unknown;
}
interface Board {
  rows: Row[];
  me: Row | null;
}
export function CatchLeaderboard() {
  const t = useCatchText();
  const [window, setWindow] = useState('7d');
  const query = useQuery({
    queryKey: ['user', 'games', 'steadycatch', 'leaderboard', window],
    queryFn: async ({ signal }) =>
      (await gameRequest<Board>('/api/games/steady-catch/leaderboard?window=' + window, { signal }))
        .data,
    staleTime: 30000,
  });
  const row = (r: Row) => {
    const identity = publicIdentity(r.identity, 'ranking identity');
    return (
      <tr key={`${r.rank}-${r.is_me ? 'me' : 'row'}`}>
        <td>{formatCount(r.rank).display}</td>
        <td
          title={
            identity.kind === 'public' ? identity.displayName : t('匿名玩家', 'Anonymous player')
          }
        >
          <PublicGameIdentity
            identity={identity}
            isMe={r.is_me}
            anonymousLabel={t('匿名玩家', 'Anonymous player')}
            meLabel={t('你', 'you')}
          />
        </td>
        <td>{formatCount(r.score).display}</td>
      </tr>
    );
  };
  const extraMe =
    query.data?.me && !query.data.rows.some((item) => item.is_me) ? [query.data.me] : [];
  const allRows = [...(query.data?.rows ?? []), ...extraMe];
  return (
    <section className="catch-ranking" id="game-rankings">
      <header>
        <h2>{t('接梗高手榜', 'Catch leaderboard')}</h2>
        <GamePrivacyControl />
      </header>
      <div className="catch-actions">
        <Segmented
          label={t('榜单周期', 'Leaderboard period')}
          value={window}
          onChange={setWindow}
          options={[
            { value: '7d', label: t('近 7 天', 'Last 7 days') },
            { value: '30d', label: t('近 30 天', 'Last 30 days') },
          ]}
        />
        <button
          type="button"
          className="nb-btn nb-btn--secondary nb-btn--sm"
          disabled={query.isFetching}
          onClick={() => void query.refetch()}
        >
          {t('刷新榜单', 'Refresh')}
        </button>
      </div>
      <p>
        {t(
          '每人取最高分；同分更早达成者优先。',
          'Each player’s best score; earlier achievement breaks ties.',
        )}
      </p>
      {query.isPending && <LoadingState />}
      {query.error && <ErrorState error={query.error} onRetry={() => void query.refetch()} />}
      {allRows.length > 0 ? (
        <div className="catch-ranking__scroll">
          <table>
            <thead>
              <tr>
                <th>#</th>
                <th>{t('玩家', 'Player')}</th>
                <th>{t('分数', 'Score')}</th>
              </tr>
            </thead>
            <tbody>{allRows.map(row)}</tbody>
          </table>
        </div>
      ) : query.data ? (
        <p>{t('暂无成绩', 'No results yet')}</p>
      ) : null}
    </section>
  );
}
