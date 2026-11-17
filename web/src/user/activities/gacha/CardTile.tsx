import type { CSSProperties } from 'react';
import { Artwork } from './Artwork';
import { ArtworkBadge } from '@shared/artwork/ArtworkImage';
import type { Card } from './types';

export function CardTile({
  card,
  owned,
  note,
  onClick,
  index = 0,
  english = false,
  showRating = true,
}: {
  card: Card;
  owned?: boolean;
  note?: string;
  onClick?: () => void;
  index?: number;
  english?: boolean;
  showRating?: boolean;
}) {
  const inner = (
    <>
      <Artwork card={card} />
      {showRating && <ArtworkBadge artworkKey={'card:' + card.id} />}
      <span className="gacha-card__rarity">{card.rarity}</span>
      <span className="gacha-card__footer">
        <span>{english ? card.name_en.split(' · ')[0] : card.name.split('·')[0]}</span>
        <strong>{card.character}</strong>
        {note && <small>{note}</small>}
      </span>
      {owned && (
        <span className="gacha-card__owned" aria-hidden="true">
          <svg viewBox="0 0 24 24">
            <path d="m5 12 4 4L19 6" />
          </svg>
        </span>
      )}
    </>
  );
  const className = 'gacha-card gacha-card--' + card.rarity + (owned ? ' is-owned' : '');
  const style = { '--card-index': index } as CSSProperties;
  const ariaLabel = (english ? card.name_en : card.name) + (note ? ', ' + note : '');
  if (!onClick) {
    return (
      <div className={className + ' is-static'} style={style} aria-label={ariaLabel}>
        {inner}
      </div>
    );
  }
  return (
    <button className={className} style={style} onClick={onClick} aria-label={ariaLabel}>
      {inner}
    </button>
  );
}
