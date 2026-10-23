export type TomorrowVariant = 'card' | 'cover' | 'illustration' | 'appearance';
export type Box = [number, number, number, number];

/** All placements use the complete source plate, including its transparent margins. */
export function tomorrowLayout(width: number, height: number, variant: TomorrowVariant) {
  const aspect = width / height;
  const sourceAspect = 1672 / 941;
  const viewWidth = Math.min(1, aspect / sourceAspect);
  const viewHeight = Math.min(1, sourceAspect / aspect);
  const view: Box = [(1 - viewWidth) * 0.55, (1 - viewHeight) * 0.55, viewWidth, viewHeight];
  let heroHeight = 0.94,
    top = 0.025,
    center = 0.69;
  if (variant === 'card') {
    heroHeight = 1.45;
    top = 0.01;
    center = 0.5;
  }
  if (variant === 'cover') {
    heroHeight = 1.8;
    top = 0.015;
    center = 0.66;
  }
  if (variant === 'appearance') {
    heroHeight = aspect < 0.85 ? 0.78 : 0.95;
    top = aspect < 0.85 ? 0.2 : 0.025;
    center = aspect < 0.85 ? 0.57 : 0.83;
  }
  const heroWidth = (heroHeight * (2 / 3)) / aspect;
  const hero: Box = [center - heroWidth * 0.49, top, heroWidth, heroHeight];
  const cloudWidth = heroWidth * 0.24;
  const cloud: Box = [
    hero[0] + heroWidth * (variant === 'card' ? 0.23 : 0.05),
    top + heroHeight * 0.16,
    cloudWidth,
    (cloudWidth * aspect) / 1.4,
  ];
  if (variant === 'card') {
    cloud[0] = 0.015;
    cloud[1] = 0.255;
    cloud[2] = 0.25;
    cloud[3] = (0.25 * aspect) / 1.4;
  }
  return { view, hero, cloud };
}
