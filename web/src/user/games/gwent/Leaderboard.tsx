import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { ErrorState } from '@shared/components/States';
import { gameRequest } from '../common/request';
import { PublicGameIdentity } from '../common/PublicGameIdentity';
import { GamePrivacyControl } from '../common/GamePrivacyControl';
import { publicIdentity } from '../common/strict';
import { useGwentText } from './copy';

interface Row {
  rank: string;
  wins: string;
  played: string;
  draws: string;
  is_me: boolean;
  identity: unknown;
}
interface Board {
  window: string;
  rows: Row[];
  me: Row | null;
}
export function GwentLeaderboard() {
  const t = useGwentText();
  const [window, setWindow] = useState('7d');
  const query = useQuery({
    queryKey: ['user', 'games', 'gwent', 'leaderboard', window],
    queryFn: async ({ signal }) =>
      (
        await gameRequest<Board>(`/api/games/gwent/leaderboard?window=${window}`, {
          signal,
          expectedStatuses: [200],
        })
      ).data,
    staleTime: 30000,
  });
  const row = (item: Row) => (
    <tr key={item.rank}>
      <td>{item.rank}</td>
      <td>
        <PublicGameIdentity
          identity={publicIdentity(item.identity, 'ranking identity')}
          anonymousLabel={t('匿名玩家', 'Anonymous player')}
          isMe={item.is_me}
          meLabel={t('你', 'you')}
        />
      </td>
      <td>{item.wins}</td>
      <td>{item.played}</td>
      <td>{((Number(item.wins) / Number(item.played)) * 100).toFixed(1)}%</td>
    </tr>
  );
  return (
    <section className="gwt-leaderboard" id="game-rankings">
      <header>
        <h2>{t('胜场榜', 'Wins leaderboard')}</h2>
        <GamePrivacyControl />
      </header>
      <div className="duel-actions">
        {['7d', '30d'].map((value) => (
          <button
            key={value}
            type="button"
            className={`btn ${window === value ? 'btn-primary' : 'btn-secondary'}`}
            aria-pressed={window === value}
            onClick={() => setWindow(value)}
          >
            {value === '7d' ? t('近 7 天', 'Last 7 days') : t('近 30 天', 'Last 30 days')}
          </button>
        ))}
      </div>
      <p>
        {t('按胜场、胜率、更早达成排序。', 'Ordered by wins, win rate, then earlier achievement.')}
      </p>
      {query.error && <ErrorState error={query.error} onRetry={() => void query.refetch()} />}
      <div className="gwt-ranking-scroll">
        <table>
          <thead>
            <tr>
              <th>#</th>
              <th>{t('玩家', 'Player')}</th>
              <th>{t('胜场', 'Wins')}</th>
              <th>{t('场次', 'Played')}</th>
              <th>{t('胜率', 'Win rate')}</th>
            </tr>
          </thead>
          <tbody>
            {query.data?.rows.map(row)}
            {query.data?.me && row(query.data.me)}
          </tbody>
        </table>
      </div>
      {query.data?.rows.length === 0 && <p>{t('暂无成绩', 'No results yet')}</p>}
    </section>
  );
}
