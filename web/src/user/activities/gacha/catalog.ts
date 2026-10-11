import data from '../../../../../internal/gacha/catalog.json';
import type { Card, Theme, Rarity, DrawTier } from './types';

export const cards = data.cards as Card[];
export const themes = data.themes as Theme[];
export const characters = data.characters;
export const cardById = Object.fromEntries(cards.map((card) => [card.id, card]));
export const themeById = Object.fromEntries(themes.map((theme) => [theme.id, theme]));
export const rarities = ['N', 'R', 'SR', 'SSR'] as const;
export const displayRarities = [...rarities, 'SP'] as const;
export const tierOf = (rarity: Rarity): DrawTier => (rarity === 'SP' ? 'SSR' : rarity);
export const isRare = (rarity: Rarity) => tierOf(rarity) === 'SSR';
export const artwork = (card: Card, full = false) => (full ? card.art : card.thumb);
