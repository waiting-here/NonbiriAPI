import { useTranslation } from 'react-i18next';
import { BiddingControls, PublicCards, RewardCard, RewardDeck } from '../bidding/Cards';
import { PlayedHistory } from '../bidding/HistoryView';
import { biddingView } from '../bidding/normalize';
import type { Match } from './api';
import '../bidding/bidding.css';

export function BiddingBoard({
  match,
  onAction,
  busy,
}: {
  match: Match;
  onAction?: (action: unknown) => void;
  busy: boolean;
}) {
  const { t } = useTranslation();
  const view = biddingView(match.view);
  const you = match.seat,
    other = you === 0 ? 1 : 0;
  const locked: [boolean, boolean] = [false, false];
  locked[you] = !match.decision_id;
  locked[other] = match.ai_state === 'locked';
  return (
    <div className="bid-play">
      <div className="bid-phase">
        <strong>{t('common.companion.round', { round: match.round })} / 13</strong>
        <span>
          {t('common.companion.score', { you: view.scores[you], other: view.scores[other] })}
        </span>
      </div>
      <section className="bid-table">
        <RewardDeck view={view} side={you} round={match.round} you={you} />
        <div className="bid-rewards">
          {view.rewards
            .filter((card) => card.round === match.round)
            .map((card) => (
              <RewardCard key={card.side} card={card} />
            ))}
        </div>
        <RewardDeck view={view} side={other} round={match.round} you={you} />
        <div className="bid-pool">
          <span>{t('common.companion.pool')}</span>
          <strong>{view.pool}</strong>
        </div>
      </section>
      {onAction && (
        <BiddingControls
          key={match.id + ':' + match.round + ':' + match.phase}
          state={{ view, you, locked, phase: match.phase === 'joker' ? 'joker' : 'bid' }}
          blocked={busy}
          onAction={onAction}
        />
      )}
      <PublicCards view={view} you={you} />
      <PlayedHistory view={view} you={you} />
    </div>
  );
}
