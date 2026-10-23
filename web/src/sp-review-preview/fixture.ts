import { cardById, themeById } from '../user/activities/gacha/catalog';
import type { Card } from '../user/activities/gacha/types';
import type { SpecialTheme } from '../user/activities/gacha/special-scenes';
import { tomorrowAssets } from '../user/activities/gacha/tomorrow/assets';

export type PreviewVariant = 'card' | 'cover' | 'illustration' | 'appearance';
export type View = PreviewVariant | 'materials';
export type Mode = 'motion' | 'still';
export type PublishedAppearance = SpecialTheme | 'celestial' | 'wandering';
type EntryDetails = {
  id: string;
  name: string;
  nameEN: string;
  art: string;
  thumb: string;
  cover: string;
  illustration: string;
  note: string;
  noteEN: string;
};
export type Entry = EntryDetails &
  ({ kind: 'tomorrow' } | { kind: 'published'; card: Card; appearance: PublishedAppearance });

export const tomorrow: Entry = {
  id: 'tomorrow',
  kind: 'tomorrow',
  name: '未命名的明天·ChatGPT',
  nameEN: 'Tomorrow, Still Unwritten · ChatGPT',
  art: tomorrowAssets.card,
  thumb: '/assets/gacha/tomorrow/thumb.webp',
  cover: tomorrowAssets.cover,
  illustration: tomorrowAssets.illustration,
  note: '把昨天放进背包，向还没有答案的明天出发。',
  noteEN: 'Pack a little of yesterday, and step into a tomorrow still full of questions.',
};
const published = (id: string): Entry => {
  const card = cardById[id];
  const theme = themeById[card.theme];
  return {
    id: card.id,
    kind: 'published',
    card,
    appearance: card.appearance as PublishedAppearance,
    name: card.name,
    nameEN: card.name_en,
    art: card.art,
    thumb: card.thumb,
    cover: '/assets/gacha/covers/' + card.theme + '.webp',
    illustration: theme.art,
    note: theme.tagline,
    noteEN: theme.tagline_en,
  };
};
export const entries: Entry[] = [
  tomorrow,
  ...['l-chatgpt', 'm-deepseek', 'n-gemini', 'o-claude', 'p-claude', 'q-glm'].map(published),
];
export const entryById = Object.fromEntries(entries.map((entry) => [entry.id, entry]));
export const entryName = (entry: Entry, english: boolean) => (english ? entry.nameEN : entry.name);
export const appearanceArt = (entry: Entry, dark: boolean) =>
  entry.kind === 'tomorrow'
    ? dark
      ? tomorrowAssets.dark
      : tomorrowAssets.light
    : '/assets/gacha/appearances/' + entry.appearance + '-' + (dark ? 'dark' : 'light') + '.webp';
export const dialogue = [
  {
    label: ['初见', 'First meeting'],
    line: [
      '我也有点紧张。……一起往前走，好吗？',
      "I'm a little nervous too. ...Shall we go on together?",
    ],
  },
  {
    label: ['重逢', 'Reunion'],
    line: ['你来了！今天，又想和你试点新鲜的。', "You're here! Let's try something new today."],
  },
  {
    label: ['心动', 'Closer to you'],
    line: [
      '未来还没想好，但今天想靠你近一点。',
      "I'm still figuring out my future. Today, I just want to be a little closer to you.",
    ],
  },
] as const;
export const introduction = [
  '昨天已经走得很远，明天还没有名字。她把旧日的小小珍藏带在身边，回过头来，笑着向你招手：“有点紧张，也有点期待。你呢？”',
  'Yesterday is already far behind; tomorrow has yet to find its name. With a small keepsake at her side, she turns and waves to you. “A little nervous, a little excited. What about you?”',
] as const;
