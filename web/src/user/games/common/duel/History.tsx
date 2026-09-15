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
    context: { mode: string; contentHash: string },
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
  const t = useDuelText();
  const [cursor, setCursor] = useState<string | null>(null);
  const query = useQuery({
    queryKey: [...duelKeys.root(codec.game), 'rounds', id, active, cursor],
    queryFn: ({ signal }) => readRounds(codec, id, active, cursor, signal),
    retry: false,
  });
  return (
    <div>
      <p>{t('阅读日志不会暂停对局计时。', 'Reading the log does not pause the game clock.')}</p>
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
            {t('第', 'Round ')} {round.round} {t('轮', '')}
          </summary>
          {renderRound(round, you)}
          {round.timeouts.some(Boolean) && (
            <p>
              {t('超时自动操作', 'Automatic timeout action')}:{' '}
              {round.timeouts
                .map((value, seat) =>
                  value ? (seat === you ? t('你', 'You') : t('对手', 'Opponent')) : null,
                )
                .filter(Boolean)
                .join(' / ')}
            </p>
          )}
        </details>
      ))}
      {query.data?.items.length === 0 && <p>{t('尚无已结算轮次。', 'No settled rounds yet.')}</p>}
      <div className="duel-actions">
        <button
          type="button"
          className="btn btn-secondary"
          onClick={() => {
            setCursor(null);
            void query.refetch();
          }}
        >
          {t('刷新／回到首页', 'Refresh / first page')}
        </button>
        <button
          type="button"
          className="btn btn-secondary"
          disabled={!query.data?.nextCursor || query.isFetching}
          onClick={() => setCursor(query.data?.nextCursor ?? null)}
        >
          {t('下一页', 'Next page')}
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
}: Props<V, F, P, S, L, A> & { readonly id: string }) {
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
}: Props<V, F, P, S, L, A> & { readonly onClose: () => void }) {
  const t = useDuelText();
  const [cursor, setCursor] = useState<string | null>(null);
  const [id, setID] = useState<string | null>(null);
  const query = useQuery({
    queryKey: [...duelKeys.root(codec.game), 'history', 'list', cursor],
    queryFn: ({ signal }) => readHistory(codec, cursor, signal),
    retry: false,
    enabled: id === null,
  });
  return (
    <DuelDialog title={t('对局记录', 'Game history')} onClose={onClose}>
      <p>{t('保留最近30天本人的完整对局。', 'Your complete games from the last 30 days.')}</p>
      {id ? (
        <>
          <button type="button" className="btn btn-secondary" onClick={() => setID(null)}>
            {t('返回列表', 'Back to list')}
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
                  <strong>{outcomeText(item.outcome, t)}</strong>
                  <span>{new Date(item.terminalAt * 1000).toLocaleString()}</span>
                  <span>
                    {item.scores[item.you]} : {item.scores[1 - item.you]}
                  </span>
                </button>
              </li>
            ))}
          </ul>
          {query.data?.items.length === 0 && <p>{t('暂无对局记录。', 'No games yet.')}</p>}
          <div className="duel-actions">
            <button
              type="button"
              className="btn btn-secondary"
              disabled={!cursor}
              onClick={() => setCursor(null)}
            >
              {t('首页', 'First page')}
            </button>
            <button
              type="button"
              className="btn btn-secondary"
              disabled={!query.data?.nextCursor || query.isFetching}
              onClick={() => setCursor(query.data?.nextCursor ?? null)}
            >
              {t('下一页', 'Next page')}
            </button>
          </div>
        </>
      )}
    </DuelDialog>
  );
}
