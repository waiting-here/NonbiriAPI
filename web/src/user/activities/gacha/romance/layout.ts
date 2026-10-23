import { heartThiefGroundPoint } from './heart-thief-stance';

export type RomanceTheme = 'elopement' | 'heart-thief';

export function romanceAppearancePlacement(width: number, height: number, theme: RomanceTheme) {
  const desktop = width >= 1024;
  const source = theme === 'elopement' ? [1024, 1536] : [1086, 1448];
  const heroHeight = height * (desktop ? 0.75 : 0.74);
  const heroWidth = (heroHeight * source[0]) / source[1];
  const center = desktop
    ? width - Math.max(72, Math.min(184, (width - 1184) / 4)) - (theme === 'heart-thief' ? 16 : 0)
    : width * (theme === 'elopement' ? 0.6 : 0.63);
  return {
    left: center - heroWidth / 2,
    top: height * (desktop && theme === 'heart-thief' ? 0.995 : 0.975) - heroHeight,
    width: heroWidth,
    height: heroHeight,
  };
}

export function romanceBackdropPlacement(
  width: number,
  height: number,
  viewportWidth = width,
  theme: RomanceTheme = 'elopement',
) {
  if (theme === 'heart-thief') {
    const desktop = viewportWidth >= 1024;
    const scale = Math.max(width / 1536, height / 1024) * (desktop ? 1.06 : 1);
    const density = width / viewportWidth;
    const hero = romanceAppearancePlacement(viewportWidth, height / density, theme);
    const characterScale = (hero.height * density) / 1448;
    const heel = heartThiefGroundPoint(486, 1382);
    const heelX = (hero.left * density + heel[0] * characterScale) / scale;
    // The rear edge of the marble floor runs from (430,760) to (1400,925).
    const floorY = 760 + ((heelX - 430) * 165) / 970;
    const heelY = hero.top * density + heel[1] * characterScale;
    const top = desktop
      ? Math.max(
          height - 1024 * scale,
          Math.min((height - 1024 * scale) * 0.43, heelY - (floorY + 10) * scale),
        )
      : 0;
    return {
      left: desktop ? 0 : (width - 1536 * scale) * 0.48,
      top,
      scale,
    };
  }
  const scale = Math.max(width / 1536, height / 864);
  return { left: (width - 1536 * scale) * (viewportWidth < 768 ? 0.48 : 0.78), top: 0, scale };
}
