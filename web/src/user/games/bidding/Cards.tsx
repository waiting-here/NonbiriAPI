import type { Reward, BiddingView, BiddingAction } from './normalize';
import type { DuelState, Seat } from '../common/duel/types';
import { useState } from 'react';
import { useDuelText } from '../common/duel/copy';
import { cardLabel } from './labels';
export function RewardCard({ card }: { readonly card: Reward }) {
  const t = useDuelText();
  return (
    <div
      className={`bid-reward bid-reward--${card.side}`}
      data-reward-key={`${card.round}:${card.side}`}
      aria-label={`${card.side === 0 ? t('方块', 'Diamonds') : t('梅花', 'Clubs')} ${cardLabel(card.rank)}, ${card.rank * card.multiplier} ${t('分', 'points')}`}
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
        <small>{t('分', 'pts')}</small>
      </strong>
      {card.multiplier === 2 && <span className="bid-double">×2 {t('王', 'Joker')}</span>}
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
  const t = useDuelText();
  const ranks = remainingRewardRanks(view, side, round);
  const suit = side === 0 ? '♦' : '♣';
  return (
    <details
      className={`bid-reward-deck bid-reward-deck--${side} ${side === you ? 'is-you' : 'is-opponent'}`}
    >
      <summary
        aria-label={t(
          `${side === you ? '自己' : '对手'}${side === 0 ? '方块' : '梅花'}牌堆，查看剩余奖励点数`,
          `${side === you ? 'Your' : 'Opponent’s'} ${side === 0 ? 'diamonds' : 'clubs'} deck; view remaining reward ranks`,
        )}
      >
        <span aria-hidden="true">{suit}</span>
        <small>{ranks.length}</small>
      </summary>
      <div
        className="bid-reward-deck__dialog"
        role="dialog"
        aria-label={t('剩余奖励牌', 'Remaining reward cards')}
      >
        <strong>{t('剩余奖励点数', 'Remaining reward ranks')}</strong>
        <p>
          {ranks.length
            ? ranks.map((rank) => cardLabel(rank)).join(' · ')
            : t('已抽完', 'All drawn')}
        </p>
        <small>
          {t(
            '仅按点数集合展示，不代表未来顺序。',
            'Ranks only; this does not reveal the future order.',
          )}
        </small>
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
  const t = useDuelText();
  const [draft, setDraft] = useState<number | null>(null);
  const locked = state.locked[state.you];
  const selection = locked ? state.view.selected : draft;
  if (state.phase === 'joker')
    return (
      <section className="bid-decision" aria-label={t('王的选择', 'Joker decision')}>
        <h2>
          {state.view.dealer === state.you
            ? t('你的王，你的时机', 'Your joker, your moment')
            : t('等待对手决定是否使用王', 'Waiting for the opponent’s joker decision')}
        </h2>
        <p>
          {t(
            '使用王，让本轮己方奖励牌分值翻倍。整局仅能使用一次。',
            'Double your reward card this round. Your joker can be used only once per game.',
          )}
        </p>
        {state.view.dealer === state.you && (
          <div className="duel-actions">
            <button
              type="button"
              className="btn btn-primary"
              disabled={blocked || locked}
              onClick={() => onAction({ kind: 'joker', use: true })}
            >
              {t('使用王 · 奖励翻倍', 'Use joker · double reward')}
            </button>
            <button
              type="button"
              className="btn btn-secondary"
              disabled={blocked || locked}
              onClick={() => onAction({ kind: 'joker', use: false })}
            >
              {t('保留王', 'Save joker')}
            </button>
          </div>
        )}
        <div className="bid-hand-scroll">
          <PokerHand
            played={state.view.played[state.you]}
            label={t('你的十三张牌', 'Your thirteen cards')}
          />
        </div>
      </section>
    );
  return (
    <section className="bid-decision" aria-label={t('你的手牌', 'Your hand')}>
      <div className="bid-section-heading">
        <h2>{t('你的手牌', 'Your hand')}</h2>
        <span>
          {locked
            ? t('已锁定，等待对手', 'Locked, waiting for opponent')
            : t('暗选一张，确认后锁定', 'Choose privately, then lock in')}
        </span>
      </div>
      <div className="bid-hand-scroll">
        <div className="bid-hand" role="group" aria-label={t('选择出牌', 'Choose a bid')}>
          {Array.from({ length: 13 }, (_, index) => index + 1).map((card) => {
            const available = state.view.hands[state.you].includes(card);
            const played = state.view.played[state.you].includes(card);
            return (
              <button
                key={card}
                type="button"
                className={`bid-card ${selection === card ? 'is-selected' : ''} ${played ? 'is-played' : ''}`}
                aria-label={`${t('出牌', 'Bid')} ${cardLabel(card)} (${card})`}
                aria-pressed={selection === card}
                disabled={blocked || locked || !available}
                onClick={() => {
                  setDraft(card);
                  onSelect?.();
                }}
              >
                <span className="bid-card__corner">{cardLabel(card)}</span>
                <strong className="bid-card__center">◆</strong>
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
            ? t('尚未选牌', 'No card selected')
            : `${t('已选', 'Selected')} ${cardLabel(selection)} · ${selection}`}
        </span>
        <button
          type="button"
          className="btn btn-primary"
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
          {locked ? t('已锁定', 'Locked') : t('锁定出牌', 'Lock in bid')}
        </button>
      </div>
      <p className="bid-hint">
        {t(
          '超时未锁定时，自动使用手中最小牌。',
          'If time expires before you lock, your lowest remaining card is used.',
        )}
      </p>
    </section>
  );
}
function PokerHand({
  played,
  label,
}: {
  readonly played: readonly number[];
  readonly label: string;
}) {
  const t = useDuelText();
  return (
    <div className="bid-hand" role="group" aria-label={label}>
      {Array.from({ length: 13 }, (_, index) => index + 1).map((card) => {
        const isPlayed = played.includes(card);
        return (
          <button
            key={card}
            type="button"
            className={`bid-card ${isPlayed ? 'is-played' : ''}`}
            aria-label={`${label} ${cardLabel(card)} (${card})${isPlayed ? ` · ${t('已出牌', 'Played')}` : ''}`}
            disabled
          >
            <span className="bid-card__corner">{cardLabel(card)}</span>
            <strong className="bid-card__center">◆</strong>
            <small className="bid-card__corner bid-card__corner--bottom">{cardLabel(card)}</small>
          </button>
        );
      })}
    </div>
  );
}

export function PublicCards({ view, you }: { readonly view: BiddingView; readonly you: Seat }) {
  const t = useDuelText();
  const opponent = (1 - you) as Seat;
  return (
    <section className="bid-public" aria-label={t('双方牌桌', 'Both players’ hands')}>
      <div className="bid-public__seat">
        <h2>{t('对手手牌', 'Opponent’s hand')}</h2>
        <div className="bid-hand-scroll">
          <PokerHand
            played={view.played[opponent]}
            label={t('对手的十三张牌', 'Opponent’s thirteen cards')}
          />
        </div>
      </div>
      <p>
        {t(
          '灰色牌表示已经出过；双方锁定前，选牌仍保持隐藏。',
          'Grey cards have already been played; selections stay hidden until both bids are locked.',
        )}
      </p>
    </section>
  );
}
