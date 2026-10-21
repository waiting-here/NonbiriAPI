import { cards, themes } from '../activities/gacha/catalog';
import type { Card } from '../activities/gacha/types';

export interface MomentArtwork {
  key: string;
  kind: 'cover' | 'card' | 'illustration';
  name: string;
  nameEN: string;
  art: string;
  thumb: string;
  card?: Card;
}

const covers = [
  ['permanent', '永续智能', 'Perpetual intelligence'],
  ['entry', '真·抽卡模拟器', 'The Gacha Simulator'],
  ['h', '万象化身', 'Myriad forms'],
  ['i', '夏夜花火', 'Summer fireworks'],
  ['j', '童话剧场', 'Fairytale theatre'],
  ['l', '星月神话', 'A Myth of Moon and Stars'],
  ['m', '逍遥游', 'Wandering Free'],
  ['n', '镜海相逢', 'Across the Mirror Sea'],
  ['o', '琥珀誓章', 'Amber Vow'],
  ['p', '天光与共', 'Shared Daylight'],
  ['q', '偷心预告', 'A Notice to Steal Your Heart'],
];
export const momentArtworks: MomentArtwork[] = [
  ...covers.map(([id, name, nameEN]) => ({
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
export const defaultMoment = momentArtworks[0];
export const momentByKey = new Map(momentArtworks.map((item) => [item.key, item]));
