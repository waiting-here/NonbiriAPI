import { cards } from './catalog';
import type { Snapshot } from './types';

export function collectionProgress(state: Snapshot, allowed: (key: string) => boolean) {
  const offeredCards = cards.filter((card) => state.visible_themes.includes(card.theme));
  const visibleCards = offeredCards.filter((card) => allowed('card:' + card.id));
  return {
    collected: visibleCards.filter((card) => state.owned[card.id]).length,
    total: visibleCards.length,
    restricted: visibleCards.length < offeredCards.length,
  };
}
