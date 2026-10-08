export const ICON_STROKE_WIDTH = 1.8;

const SOUND_PATH = 'M11 4 6 8H3v8h3l5 4V4Zm4 4a6 6 0 0 1 0 8m3-11a10 10 0 0 1 0 14';

export const GAME_ICON_PATHS = {
  warning: 'M12 4 2.5 20h19L12 4Zm0 6v4m0 3h.01',
  help: 'M9.5 9a2.5 2.5 0 1 1 4.1 1.9C12.6 11.6 12 12 12 13.5M12 17h.01M21 12a9 9 0 1 1-18 0 9 9 0 0 1 18 0Z',
  sound: SOUND_PATH,
  'sound-on': SOUND_PATH,
  'sound-off': 'M11 4 6 8H3v8h3l5 4V4Zm5 5 6 6m0-6-6 6',
  music:
    'M9 17V5l11-2v12M9 8l11-2M9 17c0 1.7-1.3 3-3 3s-3-1.3-3-3 1.3-3 3-3 3 1.3 3 3Zm11-2c0 1.7-1.3 3-3 3s-3-1.3-3-3 1.3-3 3-3 3 1.3 3 3Z',
  history: 'M3 4v6h6M3.4 9a9 9 0 1 1-.2 5M12 7v5l3 2',
  credits:
    'M20 6c0 2.2-3.6 4-8 4S4 8.2 4 6s3.6-4 8-4 8 1.8 8 4ZM4 6v6c0 2.2 3.6 4 8 4s8-1.8 8-4V6M4 12v6c0 2.2 3.6 4 8 4s8-1.8 8-4v-6',
  trophy:
    'M7 3h10v5a5 5 0 0 1-10 0V3ZM7 5H3v2a4 4 0 0 0 4 4m10-6h4v2a4 4 0 0 1-4 4M12 13v5m-4 3v-3h8v3m-10 0h12',
  book: 'M12 5v16M3 3h4a5 5 0 0 1 5 2 5 5 0 0 1 5-2h4v16h-4a5 5 0 0 0-5 2 5 5 0 0 0-5-2H3V3Z',
  play: 'm8 5 11 7-11 7V5Z',
  more: 'M5 12h.01M12 12h.01M19 12h.01',
  pause: 'M9 5v14M15 5v14',
  spark: 'm12 3 1.4 6.6L20 12l-6.6 1.4L12 20l-1.4-6.6L4 12l6.6-2.4z',
  shield: 'm12 3 7 3v5c0 4.4-2.8 8.1-7 10-4.2-1.9-7-5.6-7-10V6l7-3Z',
} as const;

export type GameIconName = keyof typeof GAME_ICON_PATHS;

export const GAME_ICON_NAMES = Object.keys(GAME_ICON_PATHS) as GameIconName[];
