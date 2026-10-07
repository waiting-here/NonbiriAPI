import { describe, expect, it } from 'vitest';
import cards from '../../../../public/assets/gwent/interface/src/cards/cards.json';
const decksPath = '../../../../public/assets/gwent/interface/src/cards/decks.js';
const storagePath = '../../../../public/assets/gwent/interface/storage.js';
const { DeckStore, importDeck, exportDeck } = await import(decksPath);
const { deckStorage } = await import(storagePath);

describe('original Gwent deck operations', () => {
  it('exposes four complete presets and both random choices in every faction', () => {
    const store = new DeckStore(cards, deckStorage('preset-check', localStorage), {
      seedSource: () => 42,
    });
    for (const faction of ['openai', 'deepseek', 'claude', 'gemini']) {
      const profiles = store.list(faction);
      expect(profiles.map((profile: { id: string }) => profile.id)).toEqual([
        'standard-balanced',
        'standard-resource',
        'standard-bond',
        'standard-control',
        'random-preset',
        'random-theme',
      ]);
      for (const profile of profiles) {
        expect(profile.deck.faction).toBe(faction);
        const counts = profile.deck.cards.reduce(
          (
            count: { units: number; heroes: number; special: number },
            entry: { id: string; count: number },
          ) => {
            const card = cards.find((value) => value.id === entry.id)!;
            expect(card).toBeDefined();
            expect(entry.count).toBeLessThanOrEqual(card.maxCopies);
            if (card.type === 'unit' || card.type === 'hero') count.units += entry.count;
            else count.special += entry.count;
            if (card.type === 'hero') count.heroes += entry.count;
            return count;
          },
          { units: 0, heroes: 0, special: 0 },
        );
        expect(counts.units).toBeGreaterThanOrEqual(22);
        expect(counts.heroes).toBeLessThanOrEqual(4);
        expect(counts.special).toBeLessThanOrEqual(10);
      }
    }
  });

  it('keeps named custom decks isolated by account and restores all eight slots', () => {
    const first = new DeckStore(cards, deckStorage('deck-owner-a', localStorage));
    const baseline = first.profile('openai', 'standard-balanced').deck;
    for (let i = 0; i < 8; i++) first.save(baseline, { id: null, name: `版本 ${i + 1}` });
    expect(() => first.save(baseline, { id: null, name: '第九套' })).toThrow('8');
    const restored = new DeckStore(cards, deckStorage('deck-owner-a', localStorage));
    expect(
      restored.list('openai').filter((profile: { kind: string }) => profile.kind === 'custom'),
    ).toHaveLength(8);
    const other = new DeckStore(cards, deckStorage('deck-owner-b', localStorage));
    expect(
      other.list('openai').filter((profile: { kind: string }) => profile.kind === 'custom'),
    ).toHaveLength(0);
    const custom = restored
      .list('openai')
      .find((profile: { kind: string }) => profile.kind === 'custom');
    restored.remove('openai', custom.id);
    expect(
      new DeckStore(cards, deckStorage('deck-owner-a', localStorage)).list('openai'),
    ).toHaveLength(13);
  });

  it('roundtrips the original import/export format and rejects another faction', () => {
    const store = new DeckStore(cards, deckStorage(null, localStorage));
    const deck = store.profile('deepseek', 'standard-resource').deck;
    const json = exportDeck(deck, '<img src=x onerror=alert(1)>', cards);
    expect(importDeck(json, cards, 'deepseek')).toEqual({
      name: '<img src=x onerror=alert(1)>',
      deck,
    });
    expect(() => importDeck(json, cards, 'openai')).toThrow('阵营');
    expect(() => importDeck('{', cards, 'deepseek')).toThrow('JSON');
    const reloaded = new DeckStore(cards, deckStorage(null, localStorage));
    expect(
      reloaded.list('deepseek').filter((profile: { kind: string }) => profile.kind === 'custom'),
    ).toHaveLength(0);
  });
});
