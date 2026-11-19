import type { MomentArtwork } from './moments';

/** Authored scene families keep the first painted surface stable, including warm-skinned portraits. */
const themeHues: Record<string, number> = {
  a: 162,
  b: 232,
  c: 178,
  d: 37,
  e: 336,
  f: 204,
  g: 255,
  h: 37,
  i: 226,
  j: 266,
  k: 180,
  l: 231,
  m: 190,
  n: 266,
  o: 38,
  p: 17,
  q: 322,
  r: 153,
  s: 339,
  t: 354,
  u: 266,
};

export function momentHue(artwork: MomentArtwork) {
  return themeHues[artwork.card?.theme ?? artwork.key.split(':')[1]] ?? 215;
}
