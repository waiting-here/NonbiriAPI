import { useState } from 'react';
import { DuelProfile } from '../common/duel/Feedback';
import type { Pair, Profile, Seat } from '../common/duel/types';
import { CardInspector, CardTile } from './Cards';
import { choiceName, factionName, rowName, useGwentText } from './copy';
import type { Action, Card, Player, View } from './types';

export function Battlefield({
  view,
  profiles,
  you = 0,
  disabled,
  onAction,
}: {
  view: View;
  profiles?: Pair<Profile>;
  you?: Seat;
  disabled: boolean;
  onAction: (action: Action) => void;
}) {
  const t = useGwentText();
  const [inspected, setInspected] = useState<Card>();
  const [zone, setZone] = useState<'self' | 'enemy' | 'known' | null>(null);
  const allCards = [
    ...view.hand,
    ...view.board.flatMap((row) => [...row.cards, ...(row.special ? [row.special] : [])]),
    ...view.self.grave,
    ...view.enemy.grave,
    ...view.weather,
    ...(view.choice?.cards ?? []),
    view.self.leader,
    view.enemy.leader,
  ];
  const selected =
    allCards.find((card) => card.instance_id === inspected?.instance_id) ?? inspected;
  const actions = view.legal_actions.filter(
    (action) =>
      selected &&
      (action.card === selected.instance_id ||
        (action.kind === 'leader' && selected.instance_id === view.self.leader.instance_id)),
  );
  const tile = (card: Card) => (
    <CardTile
      key={card.instance_id}
      card={card}
      selected={card.instance_id === selected?.instance_id}
      playable={view.legal_actions.some((action) => action.card === card.instance_id)}
      onSelect={() => setInspected(card)}
    />
  );
  const player = (side: 'self' | 'enemy', value: Player) => (
    <div className={`gwt-player is-${side}`}>
      {profiles && (
        <DuelProfile profile={profiles[side === 'self' ? you : 1 - you]} you={side === 'self'} />
      )}
      <button
        type="button"
        className="gwt-leader"
        onClick={() => setInspected(value.leader)}
        title={value.leader.name}
      >
        {factionName[value.faction]} <span>{value.leader_available ? '✦' : '◇'}</span>
      </button>
      <span aria-label={t('生命', 'Lives')}>
        {'♥'.repeat(value.lives)}
        {'♡'.repeat(Math.max(0, 2 - value.lives))}
      </span>
      <strong className="gwt-total">
        {view.board.filter((row) => row.side === side).reduce((sum, row) => sum + row.total, 0)}
      </strong>
      <span>
        {t('手牌', 'Hand')} {value.hand_count} · {t('牌库', 'Deck')} {value.deck_count}
      </span>
      <button
        type="button"
        className="gwt-zone-button"
        onClick={() => setZone(zone === side ? null : side)}
      >
        {t('弃牌', 'Discard')} {value.grave.length}
      </button>
      {value.boost > 0 && (
        <span>
          {t('下张强化', 'Next boost')} +{value.boost}
        </span>
      )}
      {value.shield && <span>{t('防护', 'Shield')} ◈</span>}
      {value.passed && <strong>{t('已放弃', 'Passed')}</strong>}
    </div>
  );
  return (
    <div className="gwt-battlefield">
      <div className="gwt-table">
        {player('enemy', view.enemy)}
        <div className="gwt-rows">
          {view.board.map((row) => (
            <section
              key={`${row.side}:${row.row}`}
              className={`gwt-row is-${row.side}${row.weather ? ' has-weather' : ''}`}
              aria-label={`${row.side === 'self' ? t('己方', 'Your') : t('敌方', 'Opponent')} ${rowName(row.row, t)}`}
            >
              <div className="gwt-row-label">
                <span className="gwt-row-name">{rowName(row.row, t)}</span>
                <span className="gwt-row-symbol" title={rowName(row.row, t)} aria-hidden="true">
                  {{ close: '⚔', ranged: '◎', siege: '▦' }[row.row]}
                </span>
                <strong>{row.total}</strong>
                {row.weather && <span title={t('天气影响', 'Weather affected')}>☁</span>}
              </div>
              <div className="gwt-row-cards">
                {row.cards.map(tile)}
                {row.special && <div className="gwt-special-slot">{tile(row.special)}</div>}
              </div>
            </section>
          ))}
        </div>
        {player('self', view.self)}
        <div className="gwt-hand" aria-label={t('你的手牌', 'Your hand')}>
          {view.hand.map(tile)}
        </div>
        {view.choice && (
          <section className="gwt-choice" aria-label={t('当前选择', 'Current choice')}>
            <strong>{choiceName(view.choice.kind, t)}</strong>
            {view.choice.remaining > 1 && (
              <span>
                {t('还可选择', 'Choices left')} {view.choice.remaining}
              </span>
            )}
            {view.choice.kind !== 'mulligan' && view.choice.kind !== 'context_window' && (
              <div className="gwt-choice-cards">{view.choice.cards.map(tile)}</div>
            )}
            <div className="gwt-choice-actions">
              {view.legal_actions
                .filter((action) => !action.card)
                .map((action, index) => (
                  <button
                    type="button"
                    key={index}
                    className="btn btn-primary"
                    disabled={disabled}
                    onClick={() => onAction(action)}
                  >
                    {action.row ? rowName(action.row, t) : t('继续', 'Continue')}
                  </button>
                ))}
            </div>
          </section>
        )}
      </div>
      <aside className="gwt-side">
        {zone ? (
          <section className="gwt-zone">
            <button type="button" className="btn btn-secondary" onClick={() => setZone(null)}>
              {t('收起牌堆', 'Close pile')}
            </button>
            <div className="gwt-zone-cards">
              {(zone === 'known' ? (view.self.known_deck_top ?? []) : view[zone].grave).map(tile)}
            </div>
          </section>
        ) : null}
        <CardInspector card={selected} actions={actions} disabled={disabled} onAction={onAction} />
        {!!view.self.known_deck_top?.length && (
          <button
            type="button"
            className="btn btn-secondary"
            onClick={() => setZone(zone === 'known' ? null : 'known')}
          >
            {t('查看已知顶牌', 'Known deck top')}
          </button>
        )}
        {!!view.weather.length && (
          <details className="gwt-weather">
            <summary>
              {t('当前天气', 'Weather')} · {view.weather.length}
            </summary>
            <div className="gwt-zone-cards">{view.weather.map(tile)}</div>
          </details>
        )}
        {!!view.rounds.length && (
          <details className="gwt-rounds">
            <summary>{t('小局成绩', 'Round results')}</summary>
            {view.rounds.map((round) => (
              <p key={round.round}>
                {t('第', 'Round')} {round.round}: {round.scores.join(' : ')}
              </p>
            ))}
          </details>
        )}
      </aside>
    </div>
  );
}
