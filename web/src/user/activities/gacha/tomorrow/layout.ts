export type TomorrowVariant = 'card' | 'cover' | 'illustration' | 'appearance';
export type Box = [number, number, number, number];

/** All placements use the complete source plate, including its transparent margins. */
export function tomorrowLayout(
  width: number,
  height: number,
  variant: TomorrowVariant,
  refined = false,
  walking = false,
) {
  const aspect = width / height;
  const sourceAspect = 1672 / 941;
  const viewWidth = Math.min(1, aspect / sourceAspect);
  const viewHeight = Math.min(1, sourceAspect / aspect);
  const view: Box = [(1 - viewWidth) * 0.55, (1 - viewHeight) * 0.55, viewWidth, viewHeight];
  let heroHeight = 0.94,
    top = 0.025,
    center = 0.69;
  if (variant === 'card') {
    heroHeight = refined ? 1.35 : 1.45;
    top = refined ? 0.015 : 0.01;
    center = refined ? 0.495 : 0.5;
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
  if (walking) {
    if (variant === 'card') {
      heroHeight = 1.32;
      top = 0.0167;
      center = 0.512;
    }
    if (variant === 'cover') {
      heroHeight = 1.7;
      top = 0.025;
      center = 0.67;
    }
    if (variant === 'illustration') {
      heroHeight = 0.94;
      top = 0.025;
      center = 0.67;
    }
    if (variant === 'appearance') {
      heroHeight = aspect < 0.85 ? 0.77 : 0.94;
      top = aspect < 0.85 ? 0.2 : 0.025;
      center = aspect < 0.85 ? 0.54 : 0.82;
    }
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
    if (refined) {
      cloud[0] = 0.025;
      cloud[1] = 0.278;
      cloud[2] = 0.22;
      cloud[3] = (0.22 * aspect) / (512 / 402);
    }
  }
  if (walking) {
    cloud[3] = (cloud[2] * aspect) / (512 / 402);
    if (variant === 'card') {
      cloud[0] = 0.03;
      cloud[1] = 0.272;
      cloud[2] = 0.2;
      cloud[3] = (0.2 * aspect) / (512 / 402);
    } else {
      cloud[0] = hero[0] + heroWidth * 0.055;
      cloud[1] = top + heroHeight * 0.225;
    }
  }
  return { view, hero, cloud };
}
