const base = '/assets/gacha/tomorrow/';
export const tomorrowAssets = {
  card: base + 'card.webp',
  cover: base + 'cover.webp',
  illustration: base + 'illustration.webp',
  light: base + 'appearance-light.webp',
  dark: base + 'appearance-dark.webp',
  hero: base + 'hero.webp',
  heroDark: base + 'hero-dark.webp',
  closed: base + 'closed.webp',
  backgroundLight: base + 'background-light.webp',
  backgroundDark: base + 'background-dark.webp',
  cloud: base + 'cloud.webp',
};

/** Portrait study; other formats retain the previous composition. */
export const refinedTomorrowAssets = {
  ...tomorrowAssets,
  card: base + 'refined/card.webp',
  hero: base + 'refined/hero.webp',
  closed: base + 'refined/closed.webp',
  backgroundLight: base + 'refined/background-light.webp',
};
