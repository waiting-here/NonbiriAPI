import { useState } from 'react';
import { CardInspector, CardTile } from './Cards';
import { factionName, useGwentText } from './copy';
import { deckCounts, FACTIONS, starterDeck, type CardDefinition, type Deck } from './types';

export function DeckEditor({
  catalog,
  deck,
  disabled,
  onChange,
}: {
  catalog: CardDefinition[];
  deck: Deck;
  disabled: boolean;
  onChange: (value: Deck) => void;
}) {
  const t = useGwentText();
  const [search, setSearch] = useState('');
  const [onlySelected, setOnlySelected] = useState(false);
  const [selected, setSelected] = useState<CardDefinition>();
  const counts = deckCounts(deck, catalog);
  const cards = catalog.filter(
    (card) =>
      !card.generated &&
      card.type !== 'leader' &&
      (card.faction === deck.faction || card.faction === 'neutral') &&
      card.name.toLowerCase().includes(search.toLowerCase()) &&
      (!onlySelected || deck.cards.some((entry) => entry.id === card.id)),
  );
  function adjust(card: CardDefinition, step: number) {
    const count = (deck.cards.find((entry) => entry.id === card.id)?.count ?? 0) + step;
    onChange({
      ...deck,
      cards: [
        ...deck.cards.filter((entry) => entry.id !== card.id),
        ...(count > 0 ? [{ id: card.id, count }] : []),
      ],
    });
  }
  return (
    <div className="gwt-deck-editor">
      <fieldset disabled={disabled} className="gwt-deck-options">
        <legend>{t('选择卡组', 'Choose your deck')}</legend>
        <div className="gwt-factions">
          {FACTIONS.map((faction) => (
            <button
              type="button"
              key={faction}
              className={`btn ${deck.faction === faction ? 'btn-primary' : 'btn-secondary'}`}
              aria-pressed={deck.faction === faction}
              onClick={() => onChange(starterDeck(faction, catalog))}
            >
              {factionName[faction]}
            </button>
          ))}
        </div>
        <label>
          {t('领袖', 'Leader')}{' '}
          <select
            value={deck.leader}
            onChange={(event) => onChange({ ...deck, leader: event.target.value })}
          >
            {catalog
              .filter((card) => card.type === 'leader' && card.faction === deck.faction)
              .map((card) => (
                <option key={card.id} value={card.id}>
                  {card.name}
                </option>
              ))}
          </select>
        </label>
        <p className="gwt-deck-counts">
          {t('单位', 'Units')} {counts.units}/22+ · {t('英雄', 'Heroes')} {counts.heroes}/4 ·{' '}
          {t('特殊牌', 'Specials')} {counts.specials}/10
        </p>
      </fieldset>
      <details className="gwt-deck-customize">
        <summary>{t('编辑卡组与查看技能', 'Edit deck and inspect abilities')}</summary>
        <div className="gwt-deck-tools">
          <label>
            {t('搜索卡牌', 'Find cards')}{' '}
            <input
              value={search}
              onChange={(event) => setSearch(event.target.value)}
              type="search"
            />
          </label>
          <label>
            <input
              type="checkbox"
              checked={onlySelected}
              onChange={(event) => setOnlySelected(event.target.checked)}
            />
            {t('只看已选', 'Selected only')}
          </label>
          <button
            type="button"
            className="btn btn-secondary"
            disabled={disabled}
            onClick={() => onChange(starterDeck(deck.faction, catalog))}
          >
            {t('恢复初始卡组', 'Reset starter deck')}
          </button>
        </div>
        <div className="gwt-deck-workspace">
          <div className="gwt-deck-grid">
            {cards.map((card) => {
              const count = deck.cards.find((entry) => entry.id === card.id)?.count ?? 0;
              return (
                <div className="gwt-deck-item" key={card.id}>
                  <CardTile
                    card={card}
                    selected={selected?.id === card.id}
                    onSelect={() => setSelected(card)}
                  />
                  <div>
                    <button
                      type="button"
                      className="btn btn-secondary"
                      aria-label={`${t('移除', 'Remove')} ${card.name}`}
                      disabled={disabled || count === 0}
                      onClick={() => adjust(card, -1)}
                    >
                      −
                    </button>
                    <span>
                      {count}/{card.maxCopies}
                    </span>
                    <button
                      type="button"
                      className="btn btn-secondary"
                      aria-label={`${t('添加', 'Add')} ${card.name}`}
                      disabled={disabled || count >= card.maxCopies}
                      onClick={() => adjust(card, 1)}
                    >
                      +
                    </button>
                  </div>
                </div>
              );
            })}
          </div>
          <CardInspector card={selected ?? catalog.find((card) => card.id === deck.leader)} />
        </div>
      </details>
    </div>
  );
}
