import { cards, themes } from '../activities/gacha/catalog';
import type { Card } from '../activities/gacha/types';
import { artworkCovers, artworkCatalog } from '@shared/artwork/catalog';

export interface MomentArtwork {
  key: string;
  kind: 'cover' | 'card' | 'illustration';
  name: string;
  nameEN: string;
  art: string;
  thumb: string;
  card?: Card;
}
export const momentArtworkKeys = (item: MomentArtwork): string[] =>
  artworkCatalog.momentKeys(item.key);

export const momentArtworks: MomentArtwork[] = [
  ...artworkCovers.map(({ id, name, name_en: nameEN }) => ({
    key: 'cover:' + id,
    kind: 'cover' as const,
    name,
    nameEN,
    art: '/assets/gacha/covers/' + id + '.webp',
    thumb: '/assets/gacha/covers/' + id + '.webp',
  })),
  ...cards.map((card) => ({
    key: 'card:' + card.id,
    kind: 'card' as const,
    name: card.name,
    nameEN: card.name_en,
    art: card.art,
    thumb: card.thumb,
    card,
  })),
  ...themes.map((theme) => ({
    key: 'illustration:' + theme.id,
    kind: 'illustration' as const,
    name: theme.name,
    nameEN: theme.name_en,
    art: theme.art,
    thumb: theme.art,
  })),
];
export const defaultMoment = momentArtworks.find((item) => item.key === 'cover:permanent')!;
export const momentByKey = new Map(momentArtworks.map((item) => [item.key, item]));
