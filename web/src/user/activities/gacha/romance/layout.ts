import { heartThiefGroundPoint } from './heart-thief-stance';

export type RomanceTheme = 'elopement' | 'heart-thief';

export function romanceAppearancePlacement(width: number, height: number, theme: RomanceTheme) {
  const desktop = width >= 1024;
  const source = theme === 'elopement' ? [1024, 1536] : [1086, 1448];
  // Keep an adult scale beside the 404 px console, including wide viewports.
  const heroHeight =
    theme === 'heart-thief'
      ? desktop
        ? Math.max(height * 0.92, Math.max(width / 1536, height / 1024) * 1.06 * 1448 * 0.58)
        : height * 0.86
      : height * (desktop ? 0.75 : 0.74);
  const heroWidth = (heroHeight * source[0]) / source[1];
  // A tall character plate still leaves room for the flower and hair beside the face.
  const headMargin = heroWidth * (theme === 'elopement' ? 0.22 : 0.16);
  const sideMargin = Math.max(72, Math.min(184, (width - 1184) / 4));
  const center = desktop
    ? width - Math.max(headMargin, sideMargin + (theme === 'heart-thief' ? 16 : 0))
    : width * (theme === 'elopement' ? 0.6 : 0.63);
  return {
    left: center - heroWidth / 2,
    top:
      theme === 'heart-thief' && desktop
        ? Math.max(height * 0.075, height * 0.995 - heroHeight)
        : height * 0.975 - heroHeight,
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
    const top =
      viewportWidth >= 640
        ? Math.max(
            height - 1024 * scale,
            Math.min((height - 1024 * scale) * 0.43, heelY - (floorY + 52) * scale),
          )
        : 0;
    return {
      left: desktop ? 0 : (width - 1536 * scale) * 0.48,
      top,
      scale,
    };
  }
  const scale = Math.max(width / 1536, height / 864);
  return {
    left: (width - 1536 * scale) * (viewportWidth < 768 ? 0.48 : 0.78),
    top: viewportWidth >= 640 ? height - 864 * scale : 0,
    scale,
  };
}

// Both landscape cameras retain the floor below the window and the entire supporting shoe.
export function heartThiefLandscapePlacement(cover = false) {
  return {
    left: cover ? 785 : 780,
    top: cover ? 12 : 26,
    scale: cover ? 0.57 : 0.55,
    backgroundTop: -160,
  };
}
