import type { DuelCodec } from '../common/duel/types';

export const FACTIONS = ['openai', 'deepseek', 'claude', 'gemini'] as const;
export type Faction = (typeof FACTIONS)[number];
export interface CardDefinition {
  id: string;
  name: string;
  faction: Faction | 'neutral';
  type: string;
  power: number;
  row: string;
  abilities: string[];
  maxCopies: number;
  image: string;
  starterCopies: number;
  generated?: boolean;
}
export interface Card extends CardDefinition {
  instance_id: number;
  base_power: number;
}
export interface Deck {
  faction: Faction;
  leader: string;
  cards: { id: string; count: number }[];
}
export interface Action {
  kind: string;
  card?: number;
  row?: string;
}
export interface Player {
  faction: Faction;
  lives: number;
  passed: boolean;
  hand_count: number;
  deck_count: number;
  grave: Card[];
  leader: Card;
  leader_available: boolean;
  boost: number;
  shield: boolean;
  known_deck_top?: Card[];
}
export interface BoardRow {
  side: 'self' | 'enemy';
  row: string;
  total: number;
  weather: boolean;
  cards: Card[];
  special?: Card;
}
export interface Round {
  round: number;
  scores: [number, number];
  winner: number | null;
  rows?: { row: string; scores: [number, number] }[];
  lives_before?: [number, number];
  lives_after?: [number, number];
  actions?: { seat: number; action: Action; automatic?: boolean }[];
}
export interface View {
  version: number;
  round: number;
  phase: string;
  turn: number;
  self: Player;
  enemy: Player;
  hand: Card[];
  board: BoardRow[];
  weather: Card[];
  choice?: { kind: string; cards: Card[]; rows: string[]; remaining: number; can_quit: boolean };
  legal_actions: Action[];
  rounds: Round[];
  result?: { winner: number | null; reason: string };
}
export interface Catalog {
  content_hash: string;
  modes: {
    standard: { rules_version: number; cards: CardDefinition[] };
    ai?: { rules_version: number; cards: CardDefinition[] };
  };
}

export const gwentCodec: DuelCodec<View, Round, never, never, Deck, Action> = {
  game: 'gwent',
  modes: ['standard', 'ai'],
  view: (value) => value as View,
  facts: (value) => value as Round,
  loadout: (value) => value as Deck,
  action: (value) => value as Action,
};

export function starterDeck(faction: Faction, catalog: readonly CardDefinition[]): Deck {
  return {
    faction,
    leader: `${faction}_leader`,
    cards: catalog
      .filter(
        (card) =>
          !card.generated &&
          card.type !== 'leader' &&
          (card.faction === faction || card.faction === 'neutral') &&
          card.starterCopies > 0,
      )
      .map((card) => ({ id: card.id, count: card.starterCopies })),
  };
}
export function deckCounts(deck: Deck, catalog: readonly CardDefinition[]) {
  const cards = new Map(catalog.map((card) => [card.id, card]));
  return deck.cards.reduce(
    (sum, entry) => {
      const card = cards.get(entry.id);
      if (card?.type === 'unit' || card?.type === 'hero') sum.units += entry.count;
      else sum.specials += entry.count;
      if (card?.type === 'hero') sum.heroes += entry.count;
      return sum;
    },
    { units: 0, specials: 0, heroes: 0 },
  );
}
