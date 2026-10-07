import { cardArt, help, rowName, useGwentText } from './copy';
import type { Action, Card, CardDefinition } from './types';

export function CardTile({
  card,
  selected = false,
  playable = false,
  onSelect,
}: {
  card: CardDefinition | Card;
  selected?: boolean;
  playable?: boolean;
  onSelect: () => void;
}) {
  return (
    <button
      type="button"
      className={`gwt-card is-${card.faction}${selected ? ' is-selected' : ''}${playable ? ' is-playable' : ''}`}
      aria-pressed={selected}
      onClick={onSelect}
      title={card.name}
    >
      <img src={cardArt(card)} alt="" loading="lazy" decoding="async" draggable={false} />
      <strong className="gwt-card__power">
        {card.type === 'unit' || card.type === 'hero' ? card.power : '✦'}
      </strong>
      <span className="gwt-card__name">{card.name}</span>
      {card.type === 'hero' && (
        <span className="gwt-card__hero" aria-hidden="true">
          ★
        </span>
      )}
    </button>
  );
}
export function CardInspector({
  card,
  actions = [],
  disabled = false,
  onAction,
}: {
  card?: CardDefinition | Card;
  actions?: readonly Action[];
  disabled?: boolean;
  onAction?: (action: Action) => void;
}) {
  const t = useGwentText();
  if (!card)
    return (
      <div className="gwt-inspector gwt-inspector--empty">
        {t(
          '点选卡牌查看技能与可用操作。',
          'Select a card to inspect its abilities and available moves.',
        )}
      </div>
    );
  return (
    <section className="gwt-inspector" aria-label={t('卡牌详情', 'Card details')}>
      <img className="gwt-inspector__art" src={cardArt(card)} alt="" />
      <h3>{card.name}</h3>
      <p>
        {rowName(card.row, t)} ·{' '}
        {card.type === 'hero'
          ? t('英雄', 'Hero')
          : card.type === 'leader'
            ? t('领袖', 'Leader')
            : t('战力', 'Power')}{' '}
        {card.power}
      </p>
      {card.abilities.map((id) => (
        <div key={id}>
          <strong>{help[id]?.name ?? id}</strong>
          <p>{help[id]?.description}</p>
        </div>
      ))}
      {!!actions.length && (
        <div className="gwt-card-actions">
          {actions.map((action, index) => (
            <button
              key={index}
              type="button"
              className="btn btn-primary"
              disabled={disabled}
              onClick={() => onAction?.(action)}
            >
              {action.kind === 'choose'
                ? t('选择', 'Choose')
                : action.kind === 'swap'
                  ? t('换牌', 'Replace')
                  : action.kind === 'leader'
                    ? t('使用领袖', 'Use leader')
                    : t('出牌', 'Play')}
              {action.row ? ` · ${rowName(action.row, t)}` : ''}
            </button>
          ))}
        </div>
      )}
    </section>
  );
}
