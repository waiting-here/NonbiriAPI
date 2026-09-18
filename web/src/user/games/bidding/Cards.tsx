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
      aria-label={`${card.side === 0 ? t('红心', 'Hearts') : t('黑桃', 'Spades')} ${cardLabel(card.rank)}, ${card.rank * card.multiplier} ${t('分', 'points')}`}
    >
      <span className="bid-reward__corner">
        {cardLabel(card.rank)}
        <small>{card.side === 0 ? '♥' : '♠'}</small>
      </span>
      <span className="bid-reward__suit" aria-hidden="true">
        {card.side === 0 ? '♥' : '♠'}
      </span>
      <strong>
        {card.rank * card.multiplier}
        <small>{t('分', 'pts')}</small>
      </strong>
      {card.multiplier === 2 && <span className="bid-double">×2 {t('王', 'Joker')}</span>}
    </div>
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
      <div className="bid-hand" role="group" aria-label={t('选择出牌', 'Choose a bid')}>
        {state.view.hands[state.you].map((card) => (
          <button
            key={card}
            type="button"
            className={`bid-card ${selection === card ? 'is-selected' : ''}`}
            aria-label={`${t('出牌', 'Bid')} ${cardLabel(card)} (${card})`}
            aria-pressed={selection === card}
            disabled={blocked || locked}
            onClick={() => {
              setDraft(card);
              onSelect?.();
            }}
          >
            <strong>{cardLabel(card)}</strong>
            <small>{card}</small>
          </button>
        ))}
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
export function PublicCards({ view, you }: { readonly view: BiddingView; readonly you: Seat }) {
  const t = useDuelText();
  return (
    <details className="bid-public">
      <summary>
        {t('对手剩余手牌', 'Opponent’s remaining cards')} · {view.hands[1 - you].length}
      </summary>
      <p className="bid-mini-cards">
        {view.hands[1 - you].map((card) => (
          <span key={card}>
            {cardLabel(card)}
            <small>{card}</small>
          </span>
        ))}
      </p>
      <p>
        {t(
          '双方锁定前，对手的选牌保持隐藏。',
          'The opponent’s selection stays hidden until both bids are locked.',
        )}
      </p>
    </details>
  );
}
