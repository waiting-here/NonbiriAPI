import { useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Card, ErrorState, LoadingState } from '@shared/components/States';
import { stationSessionWrite } from '@shared/charityManagement';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';
import { useUserSession, userKeys } from '../../data';
import { patchCharityProfile } from '../../features/core/api';
import { useDuelText } from '../common/duel/copy';
import { formatCredits } from '../common/strict';
import { PublicGameIdentity } from '../common/PublicGameIdentity';
import { isNetProfitBoard, loadRanking, type RankBoard, type RankWindow } from './api';
import './ranking.css';
function CharityPrivacy() {
  const session = useUserSession(false),
    client = useQueryClient(),
    text = useDuelText();
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
        {text('ranking.stayAnonymousOnTheTrueCharityLeaderboard')}
      </label>
      <p className="table-note">{text('ranking.thisChoiceAppliesOnlyToTrueCharity')}</p>
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
  const text = useDuelText();
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
    staleTime: 10000,
  });
  const names = {
    charity: [text('ranking.trueCharity'), text('ranking.anonymousTruePhilanthropist')],
    game_charity: [text('ranking.gameCharity'), text('ranking.anonymousPhilanthropist')],
    bidding: [text('bidding.biddingProfits'), text('ranking.anonymousBidder')],
    blackjack: [text('blackjack.profitLeaderboard'), text('blackjack.anonymousCardPlayer')],
    game_net_profit: [text('ranking.gameFortuneLeaderboard'), text('ranking.anonymousTycoon')],
    fishing_net_profit: [text('ranking.luckyCatchLeaderboard'), text('ranking.anonymousAngler')],
    blackjack_net_profit: [
      text('blackjack.cardMasterLeaderboard'),
      text('blackjack.anonymousCardPlayer'),
    ],
    bidding_net_profit: [text('bidding.biddingMasters'), text('ranking.anonymousBidder')],
  };
  const help =
    board === 'charity'
      ? text('ranking.allTimeDonationCreditsIncludingAdministratorAdjustments')
      : isNetProfitBoard(board)
        ? text('ranking.overTheLast724HoursReturns', {
            value:
              board === 'game_net_profit'
                ? text('ranking.acrossAllSixGames')
                : board === 'fishing_net_profit'
                  ? text('ranking.inPondFishing')
                  : board === 'bidding_net_profit'
                    ? text('ranking.inBiddingDuel')
                    : text('ranking.inBlackjack'),
          })
        : board === 'game_charity'
          ? text('ranking.overTheLast724HoursSpending')
          : board === 'bidding'
            ? text('ranking.positiveProfitsBeforeFeesAcrossAllThree')
            : text('ranking.positiveProfitsBeforeFeesAreAddedSeparately');
  const data = !query.error && owner ? query.data : undefined;
  const rows = [...(data?.rows ?? []), ...(data?.me ? [data.me] : [])];
  return (
    <Card className="progression-ranking">
      <h2>{names[board][0]}</h2>
      <p>{help}</p>
      <div className="rank-actions">
        {board === 'bidding' || board === 'blackjack' ? (
          <label>
            {text('ranking.period')}
            <select
              value={window}
              onChange={(event) => setWindow(event.target.value as RankWindow)}
            >
              <option value="7d">{text('ranking.rolling7Days')}</option>
              <option value="30d">{text('ranking.rolling30Days')}</option>
              <option value="history">{text('ranking.allTime')}</option>
            </select>
          </label>
        ) : null}
        <button
          type="button"
          className="btn btn-secondary"
          disabled={!owner || query.isFetching}
          onClick={() => void query.refetch()}
        >
          {text('ranking.refreshLeaderboard')}
        </button>
      </div>
      {enabled && query.isPending && <LoadingState />}
      {query.error && <ErrorState error={query.error} onRetry={() => void query.refetch()} />}
      {data && board === 'bidding_net_profit' && data.rebuildStatus !== 'completed' && (
        <p role="status" className="table-note">
          {text('ranking.rebuildingTheBiddingMastersBoardHistoricalResults')}
        </p>
      )}
      {data && board === 'bidding_net_profit' && data.rebuildStatus === 'completed' && (
        <p className="table-note">
          {text('ranking.verifiableHistoryStarts')}:{' '}
          {data.historyCoverageStart === null
            ? text('ranking.unknown')
            : new Date(data.historyCoverageStart * 1000).toLocaleString()}{' '}
          · {text('ranking.knownMissingEvents')}: {data.missingEvents}
        </p>
      )}
      {data && window === 'history' && board !== 'charity' && (
        <p className="table-note">
          {text('ranking.statisticsStarted')}
          <time dateTime={new Date(data.statisticsStart * 1000).toISOString()}>
            {new Date(data.statisticsStart * 1000).toLocaleString()}
          </time>
        </p>
      )}
      {data &&
        rows.length === 0 &&
        data.rebuildStatus !== 'scanning' &&
        data.rebuildStatus !== 'publishing' &&
        data.rebuildStatus !== 'pending' && <p>{text('ranking.noQualifyingRankingsYet')}</p>}
      {rows.length > 0 && (
        <div className="rank-table-scroll">
          <table className="data-table">
            <thead>
              <tr>
                <th>{text('ranking.rank')}</th>
                <th>{text('ranking.player')}</th>
                <th>{text('ranking.credits')}</th>
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
                    <PublicGameIdentity
                      identity={row.identity}
                      anonymousLabel={names[board][1]}
                      isMe={row.isMe}
                      meLabel={text('ranking.me')}
                    />
                  </td>
                  <td>{formatCredits(row.amount)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {data?.pagination && (
        <nav className="rank-actions" aria-label={text('ranking.charityLeaderboardPages')}>
          <button
            type="button"
            className="btn btn-secondary"
            disabled={data.pagination.page === '1' || query.isFetching}
            onClick={() => setPage((BigInt(data.pagination!.page) - 1n).toString())}
          >
            {text('blackjack.previous')}
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
            {text('blackjack.next')}
          </button>
        </nav>
      )}
      {board === 'charity' ? (
        <CharityPrivacy />
      ) : (
        <p className="table-note">{text('ranking.allGameLeaderboardsShareYourGamePrivacy')}</p>
      )}
    </Card>
  );
}
