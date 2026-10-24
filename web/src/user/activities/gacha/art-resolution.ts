export const highResolutionArt = (src: string) =>
  src.replace('/assets/gacha/', '/assets/gacha/hires/');

const standardWidths: Record<string, number> = {
  '/assets/gacha/cards/r-chatgpt.webp': 1280,
  '/assets/gacha/covers/r.webp': 1920,
  '/assets/gacha/groups/r.webp': 1920,
  '/assets/gacha/appearances/tomorrow-light.webp': 1920,
  '/assets/gacha/appearances/tomorrow-dark.webp': 1920,
};

export const artSourceSet = (src: string, standardWidth = 1672, highWidth = 3840) =>
  src +
  ' ' +
  (standardWidths[src.split(/[?#]/, 1)[0]] ?? standardWidth) +
  'w, ' +
  highResolutionArt(src) +
  ' ' +
  highWidth +
  'w';
