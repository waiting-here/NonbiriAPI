import data from '../../../../../internal/gacha/catalog.json';
import type { Card, Theme } from './types';

export const cards = data.cards as Card[];
export const themes = data.themes as Theme[];
export const characters = data.characters;
export const cardById = Object.fromEntries(cards.map((card) => [card.id, card]));
export const themeById = Object.fromEntries(themes.map((theme) => [theme.id, theme]));
export const rarities = ['N', 'R', 'SR', 'SSR'] as const;
export const artwork = (card: Card, full = false) => (full ? card.art : card.thumb);
