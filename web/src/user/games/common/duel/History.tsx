import { useState, type ReactNode } from 'react';
import { RandomnessProof } from '../RandomnessProof';
import { useQuery } from '@tanstack/react-query';
import { duelKeys, readDetail, readHistory, readRounds } from './api';
import type { DuelCodec, DuelDetail, DuelRound, Seat } from './types';
import { DuelDialog } from './Dialog';
import { DuelFeedback } from './Feedback';
import { DuelFinance } from './Finance';
import { outcomeText, useDuelText } from './copy';
type Props<V, F, P, S, L, A> = {
  readonly codec: DuelCodec<V, F, P, S, L, A>;
  readonly renderRound: (
    round: DuelRound<V, F, S>,
    you: Seat,
    context: {
      mode: string;
      contentHash: string;
    },
  ) => ReactNode;
  readonly renderDetail?: (detail: DuelDetail<V, P, S, A>) => ReactNode;
};
export function DuelRoundLog<V, F, P, S, L, A>({
  codec,
  id,
  active,
  you,
  renderRound,
}: {
  readonly codec: DuelCodec<V, F, P, S, L, A>;
  readonly id: string;
  readonly active: boolean;
  readonly you: Seat;
  readonly renderRound: (round: DuelRound<V, F, S>, you: Seat) => ReactNode;
}) {
  const text = useDuelText();
  const [cursor, setCursor] = useState<string | null>(null);
  const query = useQuery({
    queryKey: [...duelKeys.root(codec.game), 'rounds', id, active, cursor],
    queryFn: ({ signal }) => readRounds(codec, id, active, cursor, signal),
    retry: false,
  });
  return (
    <div>
      <p>{text('common.readingTheLogDoesNotPauseThe')}</p>
      <DuelFeedback
        error={query.error}
        pending={query.isPending}
        onRetry={() => {
          void query.refetch();
        }}
      />
      {query.data?.items.map((round) => (
        <details className="duel-round" key={round.round}>
          <summary>
            {text('common.round')} {round.round} {text('bidding.message')}
          </summary>
          {renderRound(round, you)}
          {round.timeouts.some(Boolean) && (
            <p>
              {text('common.automaticTimeoutAction')}:{' '}
              {round.timeouts
                .map((value, seat) =>
                  value ? (seat === you ? text('bidding.you') : text('bidding.opponent')) : null,
                )
                .filter(Boolean)
                .join(' / ')}
            </p>
          )}
        </details>
      ))}
      {query.data?.items.length === 0 && <p>{text('common.noSettledRoundsYet')}</p>}
      <div className="duel-actions">
        <button
          type="button"
          className="btn btn-secondary"
          onClick={() => {
            setCursor(null);
            void query.refetch();
          }}
        >
          {text('common.refreshFirstPage')}
        </button>
        <button
          type="button"
          className="btn btn-secondary"
          disabled={!query.data?.nextCursor || query.isFetching}
          onClick={() => setCursor(query.data?.nextCursor ?? null)}
        >
          {text('common.nextPage')}
        </button>
      </div>
    </div>
  );
}
function HistoryDetail<V, F, P, S, L, A>({
  codec,
  id,
  renderRound,
  renderDetail,
}: Props<V, F, P, S, L, A> & {
  readonly id: string;
}) {
  const query = useQuery({
    queryKey: [...duelKeys.root(codec.game), 'history', id],
    queryFn: ({ signal }) => readDetail(codec, id, signal),
    retry: false,
  });
  return (
    <>
      <DuelFeedback
        error={query.error}
        pending={query.isPending}
        onRetry={() => {
          void query.refetch();
        }}
      />
      {query.data && (
        <>
          <DuelFinance result={query.data.result} />
          <RandomnessProof game={codec.game} id={id} terminal />
          {renderDetail?.(query.data)}
          <DuelRoundLog
            codec={codec}
            id={id}
            active={false}
            you={query.data.result.you}
            renderRound={(round, you) =>
              renderRound(round, you, {
                mode: query.data!.result.mode,
                contentHash: query.data!.contentHash,
              })
            }
          />
        </>
      )}
    </>
  );
}
export function DuelHistory<V, F, P, S, L, A>({
  codec,
  onClose,
  renderRound,
  renderDetail,
}: Props<V, F, P, S, L, A> & {
  readonly onClose: () => void;
}) {
  const text = useDuelText();
  const [cursor, setCursor] = useState<string | null>(null);
  const [id, setID] = useState<string | null>(null);
  const query = useQuery({
    queryKey: [...duelKeys.root(codec.game), 'history', 'list', cursor],
    queryFn: ({ signal }) => readHistory(codec, cursor, signal),
    retry: false,
    enabled: id === null,
  });
  return (
    <DuelDialog title={text('bidding.gameHistory')} onClose={onClose}>
      <p>{text('common.yourCompleteGamesFromTheLast30')}</p>
      {id ? (
        <>
          <button type="button" className="btn btn-secondary" onClick={() => setID(null)}>
            {text('blackjack.backToList')}
          </button>
          <HistoryDetail
            key={id}
            codec={codec}
            id={id}
            renderRound={renderRound}
            renderDetail={renderDetail}
          />
        </>
      ) : (
        <>
          <DuelFeedback
            error={query.error}
            pending={query.isPending}
            onRetry={() => {
              void query.refetch();
            }}
          />
          <ul className="duel-history-list">
            {query.data?.items.map((item) => (
              <li key={item.id}>
                <button type="button" onClick={() => setID(item.id)}>
                  <strong>{outcomeText(item.outcome, text)}</strong>
                  <span>{new Date(item.terminalAt * 1000).toLocaleString()}</span>
                  <span>
                    {item.scores[item.you]} : {item.scores[1 - item.you]}
                  </span>
                </button>
              </li>
            ))}
          </ul>
          {query.data?.items.length === 0 && <p>{text('blackjack.noGamesYet')}</p>}
          <div className="duel-actions">
            <button
              type="button"
              className="btn btn-secondary"
              disabled={!cursor}
              onClick={() => setCursor(null)}
            >
              {text('common.firstPage')}
            </button>
            <button
              type="button"
              className="btn btn-secondary"
              disabled={!query.data?.nextCursor || query.isFetching}
              onClick={() => setCursor(query.data?.nextCursor ?? null)}
            >
              {text('common.nextPage')}
            </button>
          </div>
        </>
      )}
    </DuelDialog>
  );
}
