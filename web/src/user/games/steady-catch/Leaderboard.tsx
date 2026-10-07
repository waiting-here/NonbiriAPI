import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { ErrorState } from '@shared/components/States';
import { gameRequest } from '../common/request';
import { PublicGameIdentity } from '../common/PublicGameIdentity';
import { GamePrivacyControl } from '../common/GamePrivacyControl';
import { publicIdentity } from '../common/strict';
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
  const row = (r: Row) => (
    <tr key={r.rank}>
      <td>{r.rank}</td>
      <td>
        <PublicGameIdentity
          identity={publicIdentity(r.identity, 'ranking identity')}
          isMe={r.is_me}
          anonymousLabel={t('匿名玩家', 'Anonymous player')}
          meLabel={t('你', 'you')}
        />
      </td>
      <td>{r.score}</td>
    </tr>
  );
  return (
    <section className="catch-ranking" id="game-rankings">
      <header>
        <h2>{t('接梗高手榜', 'Catch leaderboard')}</h2>
        <GamePrivacyControl />
      </header>
      <div className="catch-actions">
        {['7d', '30d'].map((value) => (
          <button
            className="btn btn-secondary"
            key={value}
            aria-pressed={window === value}
            onClick={() => setWindow(value)}
          >
            {value === '7d' ? t('近 7 天', 'Last 7 days') : t('近 30 天', 'Last 30 days')}
          </button>
        ))}
      </div>
      <p>
        {t(
          '每人取最高分；同分更早达成者优先。',
          'Each player’s best score; earlier achievement breaks ties.',
        )}
      </p>
      {query.error && <ErrorState error={query.error} onRetry={() => void query.refetch()} />}
      <table>
        <thead>
          <tr>
            <th>#</th>
            <th>{t('玩家', 'Player')}</th>
            <th>{t('分数', 'Score')}</th>
          </tr>
        </thead>
        <tbody>
          {query.data?.rows.map(row)}
          {query.data?.me && row(query.data.me)}
        </tbody>
      </table>
      {query.data?.rows.length === 0 && <p>{t('暂无成绩', 'No results yet')}</p>}
    </section>
  );
}
