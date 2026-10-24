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

export const turnBackTomorrowAssets = {
  card: base + 'turn-back/card.webp',
  thumb: base + 'turn-back/thumb.webp',
  hero: base + 'turn-back/hero.webp',
};

const adopted = '/assets/gacha/';
export const walkingTomorrowAssets = {
  card: adopted + 'cards/r-chatgpt.webp',
  cover: adopted + 'covers/r.webp',
  illustration: adopted + 'groups/r.webp',
  light: adopted + 'appearances/tomorrow-light.webp',
  dark: adopted + 'appearances/tomorrow-dark.webp',
  thumb: adopted + 'thumbs/r-chatgpt.webp',
  hero: adopted + 'motion/r-chatgpt/hero.webp',
  heroDark: adopted + 'motion/r-chatgpt/hero-dark.webp',
  closed: adopted + 'motion/r-chatgpt/closed.webp',
  body: adopted + 'motion/r-chatgpt/body.webp',
  pendant: adopted + 'motion/r-chatgpt/pendant.webp',
  backgroundLight: adopted + 'motion/r-chatgpt/background-light.webp',
  backgroundDark: adopted + 'motion/r-chatgpt/background-dark.webp',
  cloud: adopted + 'motion/r-chatgpt/cloud.webp',
};
