import { readFileSync, existsSync } from 'node:fs';
import { resolve, basename } from 'node:path';
import { expect, it } from 'vitest';
import { help } from './copy';
import { deckCounts, FACTIONS, starterDeck, type CardDefinition } from './types';

const cards = JSON.parse(
  readFileSync(resolve('../internal/game/gwent/engine/cards.json'), 'utf8'),
) as CardDefinition[];
it('provides all four playable starter decks with bundled card art and ability text', () => {
  for (const faction of FACTIONS) {
    const counts = deckCounts(starterDeck(faction, cards), cards);
    expect(counts.units).toBeGreaterThanOrEqual(22);
    expect(counts.heroes).toBeLessThanOrEqual(4);
    expect(counts.specials).toBeLessThanOrEqual(10);
  }
  for (const card of cards) {
    expect(existsSync(resolve('public/assets/gwent/cards', basename(card.image))), card.id).toBe(
      true,
    );
    for (const ability of card.abilities)
      expect(help[ability]?.description, `${card.id}: ${ability}`).toBeTruthy();
  }
});
