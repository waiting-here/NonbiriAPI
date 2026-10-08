import type { Reward, BiddingView, BiddingAction } from './normalize';
import type { DuelState, Seat } from '../common/duel/types';
import { useState } from 'react';
import { useDuelText } from '../common/duel/copy';
import { cardLabel, handSuit } from './labels';
export function RewardCard({ card }: { readonly card: Reward }) {
  const text = useDuelText();
  return (
    <div
      className={`bid-reward bid-reward--${card.side}`}
      data-reward-key={`${card.round}:${card.side}`}
      aria-label={`${card.side === 0 ? text('bidding.diamonds') : text('bidding.clubs')} ${cardLabel(card.rank)}, ${card.rank * card.multiplier} ${text('bidding.points')}`}
    >
      <span className="bid-reward__corner">
        {cardLabel(card.rank)}
        <small>{card.side === 0 ? '♦' : '♣'}</small>
      </span>
      <span className="bid-reward__suit" aria-hidden="true">
        {card.side === 0 ? '♦' : '♣'}
      </span>
      <strong>
        {card.rank * card.multiplier}
        <small>{text('bidding.pts')}</small>
      </strong>
      {card.multiplier === 2 && <span className="bid-double">×2 Joker</span>}
    </div>
  );
}
export function remainingRewardRanks(view: BiddingView, side: Seat, round: number) {
  const revealed = new Set(
    view.rewards
      .filter((reward) => reward.side === side && reward.round <= round)
      .map((reward) => reward.rank),
  );
  return Array.from({ length: 13 }, (_, index) => index + 1).filter((rank) => !revealed.has(rank));
}
export function RewardDeck({
  view,
  side,
  round,
  you,
}: {
  readonly view: BiddingView;
  readonly side: Seat;
  readonly round: number;
  readonly you: Seat;
}) {
  const text = useDuelText();
  const ranks = remainingRewardRanks(view, side, round);
  const suit = side === 0 ? '♦' : '♣';
  return (
    <details
      className={`bid-reward-deck bid-reward-deck--${side} ${side === you ? 'is-you' : 'is-opponent'}`}
    >
      <summary
        aria-label={text('bidding.deckViewRemainingRewardRanks', {
          value: side === you ? text('bidding.your') : text('bidding.opponentS'),
          value2: side === 0 ? text('bidding.diamonds2') : text('bidding.clubs2'),
        })}
      >
        <span aria-hidden="true">{suit}</span>
        <small>{ranks.length}</small>
      </summary>
      <div
        className="bid-reward-deck__dialog"
        role="dialog"
        aria-label={text('bidding.remainingRewardCards')}
      >
        <strong>{text('bidding.remainingRewardRanks')}</strong>
        <p>
          {ranks.length
            ? ranks.map((rank) => cardLabel(rank)).join(' · ')
            : text('bidding.allDrawn')}
        </p>
        <small>{text('bidding.ranksOnlyThisDoesNotRevealThe')}</small>
      </div>
    </details>
  );
}
export function BiddingControls({
  state,
  blocked,
  onAction,
  onSelect,
}: {
  readonly state: DuelState<BiddingView, never, never>;
  readonly blocked: boolean;
  readonly onAction: (action: BiddingAction) => void;
  readonly onSelect?: () => void;
}) {
  const text = useDuelText();
  const [draft, setDraft] = useState<number | null>(null);
  const suit = handSuit(state.you, text);
  const locked = state.locked[state.you];
  const selection = locked ? state.view.selected : draft;
  if (state.phase === 'joker')
    return (
      <section className="bid-decision" aria-label={text('bidding.jokerDecision')}>
        <h2>
          {state.view.dealer === state.you
            ? text('bidding.yourJokerYourMoment')
            : text('bidding.waitingForTheOpponentSJokerDecision')}
        </h2>
        <p>{text('bidding.doubleYourRewardCardThisRoundYour')}</p>
        {state.view.dealer === state.you && (
          <div className="duel-actions">
            <button
              type="button"
              className="nb-btn nb-btn--primary"
              disabled={blocked || locked}
              onClick={() => onAction({ kind: 'joker', use: true })}
            >
              {text('bidding.useJokerDoubleReward')}
            </button>
            <button
              type="button"
              className="nb-btn nb-btn--secondary"
              disabled={blocked || locked}
              onClick={() => onAction({ kind: 'joker', use: false })}
            >
              {text('bidding.saveJoker')}
            </button>
          </div>
        )}
        <div className="bid-hand-scroll">
          <PokerHand
            played={state.view.played[state.you]}
            seat={state.you}
            label={text('bidding.yourThirteenCards')}
          />
        </div>
      </section>
    );
  return (
    <section className="bid-decision" aria-label={text('bidding.yourHand')}>
      <div className="bid-section-heading">
        <h2>{text('bidding.yourHand')}</h2>
        <span>
          {locked
            ? text('bidding.lockedWaitingForOpponent')
            : text('bidding.choosePrivatelyThenLockIn')}
        </span>
      </div>
      <div className="bid-hand-scroll">
        <div className="bid-hand" role="group" aria-label={text('bidding.chooseABid')}>
          {Array.from({ length: 13 }, (_, index) => index + 1).map((card) => {
            const available = state.view.hands[state.you].includes(card);
            const played = state.view.played[state.you].includes(card);
            return (
              <button
                key={card}
                type="button"
                className={`bid-card bid-suit--${state.you} ${selection === card ? 'is-selected' : ''} ${played ? 'is-played' : ''}`}
                aria-label={`${text('bidding.bid')} ${suit.name} ${cardLabel(card)} (${card})`}
                aria-pressed={selection === card}
                disabled={blocked || locked || !available}
                onClick={() => {
                  setDraft(card);
                  onSelect?.();
                }}
              >
                <span className="bid-card__corner">{cardLabel(card)}</span>
                <strong className="bid-card__center" aria-hidden="true">
                  {suit.symbol}
                </strong>
                <small className="bid-card__corner bid-card__corner--bottom">
                  {cardLabel(card)}
                </small>
              </button>
            );
          })}
        </div>
      </div>
      <div className="bid-confirm">
        <span aria-live="polite">
          {selection === null
            ? text('bidding.noCardSelected')
            : `${text('bidding.selected')} ${suit.name} ${cardLabel(selection)} · ${selection}`}
        </span>
        <button
          type="button"
          className="nb-btn nb-btn--primary"
          disabled={
            blocked ||
            locked ||
            selection === null ||
            !state.view.hands[state.you].includes(selection)
          }
          onClick={() => {
            if (selection !== null) onAction({ kind: 'bid', card: selection });
          }}
        >
          {locked ? text('bidding.locked') : text('bidding.lockInBid')}
        </button>
      </div>
      <p className="bid-hint">{text('bidding.ifTimeExpiresBeforeYouLockYour')}</p>
    </section>
  );
}
function PokerHand({
  seat,
  played,
  label,
}: {
  readonly seat: Seat;
  readonly played: readonly number[];
  readonly label: string;
}) {
  const text = useDuelText();
  const suit = handSuit(seat, text);
  return (
    <div className="bid-hand" role="group" aria-label={label}>
      {Array.from({ length: 13 }, (_, index) => index + 1).map((card) => {
        const isPlayed = played.includes(card);
        return (
          <button
            key={card}
            type="button"
            className={`bid-card bid-suit--${seat} ${isPlayed ? 'is-played' : ''}`}
            aria-label={`${label} ${suit.name} ${cardLabel(card)} (${card})${isPlayed ? ` · ${text('bidding.played')}` : ''}`}
            disabled
          >
            <span className="bid-card__corner">{cardLabel(card)}</span>
            <strong className="bid-card__center" aria-hidden="true">
              {suit.symbol}
            </strong>
            <small className="bid-card__corner bid-card__corner--bottom">{cardLabel(card)}</small>
          </button>
        );
      })}
    </div>
  );
}
export function PublicCards({ view, you }: { readonly view: BiddingView; readonly you: Seat }) {
  const text = useDuelText();
  const opponent = (1 - you) as Seat;
  return (
    <section className="bid-public" aria-label={text('bidding.bothPlayersHands')}>
      <div className="bid-public__seat">
        <h2>{text('bidding.opponentSHand')}</h2>
        <div className="bid-hand-scroll">
          <PokerHand
            played={view.played[opponent]}
            seat={opponent}
            label={text('bidding.opponentSThirteenCards')}
          />
        </div>
      </div>
      <p>{text('bidding.greyCardsHaveAlreadyBeenPlayedSelections')}</p>
    </section>
  );
}
