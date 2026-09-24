import { useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Card, ErrorState, LoadingState } from '@shared/components/States';
import { stationSessionWrite } from '@shared/charityManagement';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';
import { useUserSession, userKeys } from '../../data';
import { patchCharityProfile } from '../../features/core/api';
import { useDuelText } from '../common/duel/copy';
import { formatCredits } from '../common/strict';
import { isNetProfitBoard, loadRanking, type RankBoard, type RankWindow } from './api';
import './ranking.css';

function CharityPrivacy() {
  const session = useUserSession(false),
    client = useQueryClient(),
    t = useDuelText();
  const save = useRetainedOperation(
    (isPublic: boolean, key) =>
      stationSessionWrite(client, 'steward', () =>
        patchCharityProfile(isPublic, { idempotencyKey: key, actionId: key }),
      ),
    () =>
      Promise.all([
        client.invalidateQueries({ queryKey: userKeys.session }),
        client.invalidateQueries({ queryKey: userKeys.me }),
        client.invalidateQueries({ queryKey: ['user', 'games', 'rankings'] }),
      ]),
    ['user'],
  );
  if (!session.data?.user || session.error) return null;
  return (
    <div className="rank-privacy">
      <label className="checkbox-label">
        <input
          type="checkbox"
          checked={!session.data.user.charity_profile_public}
          disabled={save.isPending}
          onChange={(event) => save.mutate(!event.target.checked)}
        />
        {t('在真·慈善榜中匿名', 'Stay anonymous on the True Charity leaderboard')}
      </label>
      <p className="table-note">
        {t(
          '此选择仅用于真·慈善榜，与游戏榜的匿名设置独立。封禁期间始终匿名，解除后恢复你的选择。',
          'This choice applies only to True Charity, independently of game privacy. Banned accounts stay anonymous and regain their saved choice when the restriction ends.',
        )}
      </p>
      {save.error && <ErrorState error={save.error} />}
    </div>
  );
}

export function Leaderboard({
  board,
  enabled = true,
}: {
  readonly board: RankBoard;
  readonly enabled?: boolean;
}) {
  const session = useUserSession(false);
  const owner = session.error ? undefined : session.data?.user.id;
  return (
    <RankingPanel
      key={`${owner ?? 'none'}:${board}`}
      owner={owner}
      board={board}
      enabled={enabled}
    />
  );
}

function RankingPanel({
  board,
  owner,
  enabled,
}: {
  readonly board: RankBoard;
  readonly owner?: string;
  readonly enabled: boolean;
}) {
  const t = useDuelText();
  const [selectedWindow, setWindow] = useState<RankWindow>('7d');
  const [page, setPage] = useState('1');
  const window =
    board === 'charity'
      ? 'history'
      : board === 'game_charity' || isNetProfitBoard(board)
        ? '7d'
        : selectedWindow;
  const query = useQuery({
    queryKey: ['user', 'games', 'rankings', owner, board, window, page],
    queryFn: ({ signal }) => loadRanking(board, window, page, signal),
    enabled: enabled && !!owner,
    staleTime: 10_000,
  });
  const names = {
    charity: [t('真·慈善榜', 'True Charity'), t('匿名真·慈善家', 'Anonymous true philanthropist')],
    game_charity: [t('游戏慈善榜', 'Game Charity'), t('匿名慈善家', 'Anonymous philanthropist')],
    bidding: [t('竞标利润榜', 'Bidding profits'), t('匿名竞标者', 'Anonymous bidder')],
    blackjack: [t('二十一点利润榜', 'Blackjack profits'), t('匿名牌手', 'Anonymous card player')],
    game_net_profit: [t('暴富榜', 'Fortune leaderboard'), t('匿名玩家', 'Anonymous player')],
    fishing_net_profit: [t('锦鲤榜', 'Lucky catch leaderboard'), t('匿名钓友', 'Anonymous angler')],
    blackjack_net_profit: [
      t('赌神榜', 'Card master leaderboard'),
      t('匿名牌手', 'Anonymous card player'),
    ],
  };
  const help =
    board === 'charity'
      ? t(
          '按全部历史捐赠积分排名，包含管理员调整。只展示正值，每页20人。',
          'All-time donation credits, including administrator adjustments. Positive totals only, twenty people per page.',
        )
      : isNetProfitBoard(board)
        ? t(
            `最近7×24小时，${board === 'game_net_profit' ? '六游戏' : board === 'fishing_net_profit' ? '池塘垂钓' : '二十一点'}实际返还减实际投入，输赢相抵并计入抽水与入场费。两种积分等值计算，排除奖励、网贷和调账等非对局收支。仅正净盈利入榜。`,
            `Over the last 7×24 hours: returns minus spending ${board === 'game_net_profit' ? 'across all six games' : board === 'fishing_net_profit' ? 'in pond fishing' : 'in blackjack'}, including fees and entry costs. Losses offset wins and both credit types count equally. Rewards, loans and other non-game transactions are excluded. Positive net profits only.`,
          )
        : board === 'game_charity'
          ? t(
              '最近7×24小时，六游戏实际支出减去抽水后返还，输赢相抵。两种积分等值计算，不含新人奖励和贷款。仅正净亏损入榜。',
              'Over the last 7×24 hours: spending minus after-fee returns across all six games. Wins offset losses and both credit types count equally. Newcomer rewards and loans are excluded. Positive net losses only.',
            )
          : board === 'bidding'
            ? t(
                '三档合计抽水前的正利润，不含本金，亏损不抵扣。',
                'Positive profits before fees across all three tiers. Principal is excluded and losses do not offset profits.',
              )
            : t(
                '每手抽水前利润分别取正值再累加，包含分牌和加倍；不以整局净赚为门槛。',
                'Positive profits before fees are added separately for each hand, including split and doubled hands. The whole round need not be profitable.',
              );
  const data = !query.error && owner ? query.data : undefined;
  const rows = [...(data?.rows ?? []), ...(data?.me ? [data.me] : [])];
  return (
    <Card className="progression-ranking">
      <h2>{names[board][0]}</h2>
      <p>{help}</p>
      <div className="rank-actions">
        {board === 'bidding' || board === 'blackjack' ? (
          <label>
            {t('统计窗口', 'Period')}
            <select
              value={window}
              onChange={(event) => setWindow(event.target.value as RankWindow)}
            >
              <option value="7d">{t('滚动7天', 'Rolling 7 days')}</option>
              <option value="30d">{t('滚动30天', 'Rolling 30 days')}</option>
              <option value="history">{t('历史', 'All time')}</option>
            </select>
          </label>
        ) : null}
        <button
          type="button"
          className="btn btn-secondary"
          disabled={!owner || query.isFetching}
          onClick={() => void query.refetch()}
        >
          {t('刷新榜单', 'Refresh leaderboard')}
        </button>
      </div>
      {enabled && query.isPending && <LoadingState />}
      {query.error && <ErrorState error={query.error} onRetry={() => void query.refetch()} />}
      {data && window === 'history' && board !== 'charity' && (
        <p className="table-note">
          {t('统计起点：', 'Statistics started: ')}
          <time dateTime={new Date(data.statisticsStart * 1000).toISOString()}>
            {new Date(data.statisticsStart * 1000).toLocaleString()}
          </time>
        </p>
      )}
      {data && rows.length === 0 && (
        <p>{t('暂无符合条件的排名。', 'No qualifying rankings yet.')}</p>
      )}
      {rows.length > 0 && (
        <div className="rank-table-scroll">
          <table className="data-table">
            <thead>
              <tr>
                <th>{t('排名', 'Rank')}</th>
                <th>{t('玩家', 'Player')}</th>
                <th>{t('积分', 'Credits')}</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((row) => (
                <tr
                  key={row.rank}
                  className={row.isMe ? 'is-me' : undefined}
                  data-own-rank={row.isMe || undefined}
                >
                  <td>{row.rank}</td>
                  <td>
                    <span className="rank-identity">
                      {row.identity.kind === 'public' && row.identity.avatarURL && (
                        <img
                          src={row.identity.avatarURL}
                          alt=""
                          width="28"
                          height="28"
                          referrerPolicy="no-referrer"
                          loading="lazy"
                        />
                      )}
                      <span>
                        {row.identity.kind === 'public'
                          ? row.identity.displayName
                          : names[board][1]}
                        {row.isMe ? ` · ${t('我', 'Me')}` : ''}
                      </span>
                    </span>
                  </td>
                  <td>{formatCredits(row.amount)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {data?.pagination && (
        <nav className="rank-actions" aria-label={t('慈善榜分页', 'Charity leaderboard pages')}>
          <button
            type="button"
            className="btn btn-secondary"
            disabled={data.pagination.page === '1' || query.isFetching}
            onClick={() => setPage((BigInt(data.pagination!.page) - 1n).toString())}
          >
            {t('上一页', 'Previous')}
          </button>
          <span>
            {data.pagination.page} / {data.pagination.total_pages}
          </span>
          <button
            type="button"
            className="btn btn-secondary"
            disabled={data.pagination.page === data.pagination.total_pages || query.isFetching}
            onClick={() => setPage((BigInt(data.pagination!.page) + 1n).toString())}
          >
            {t('下一页', 'Next')}
          </button>
        </nav>
      )}
      {board === 'charity' ? (
        <CharityPrivacy />
      ) : (
        <p className="table-note">
          {t(
            '所有游戏榜共用游戏匿名设置。封禁期间保留排名并强制匿名。',
            'All game leaderboards share your game privacy setting. Banned accounts remain ranked and anonymous.',
          )}
        </p>
      )}
    </Card>
  );
}
