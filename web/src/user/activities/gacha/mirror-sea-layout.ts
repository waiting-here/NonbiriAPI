import { mirrorSea } from './mirror-sea-config';
import { portraitView } from './portrait';

const originalAspect = 1672 / 941;
const glass = [0.704, 0.43, 0.108, 0.353] as const;
const touch = [0.145, 0.128] as const;

export function mirrorSeaAppearancePlacement(width: number, height: number) {
  const desktop = width >= 1024;
  const heroHeight = height * (desktop ? 0.75 : 0.74);
  const heroWidth = heroHeight * (1024 / 1536);
  const center = desktop ? width - Math.max(72, Math.min(184, (width - 1184) / 4)) : width * 0.62;
  const top = height * 0.975 - heroHeight;
  const left = center - heroWidth / 2;
  const vw = Math.min(1, width / height / originalAspect);
  const vh = Math.min(1, originalAspect / (width / height));
  const viewY = 1 - vh;
  // On narrower desktops the panorama follows the mirror, while the face keeps its side margin.
  const viewX = desktop
    ? Math.min(1 - vw, Math.max(0, 0.75 - ((left + heroWidth * touch[0]) / width) * vw))
    : (1 - vw) * mirrorSea.cropPosition;
  const heroBox = [
    viewX + (left / width) * vw,
    viewY + (top / height) * vh,
    (heroWidth / width) * vw,
    (heroHeight / height) * vh,
  ];
  const handY = heroBox[1] + heroBox[3] * touch[1];
  const handX = heroBox[0] + heroBox[2] * touch[0];
  const reach = glass[2] * Math.sqrt(Math.max(0, 0.9 ** 2 - ((handY - glass[1]) / glass[3]) ** 2));
  const contactX = Math.max(glass[0] - reach, Math.min(glass[0] + reach, handX));
  heroBox[0] += contactX - handX;
  return {
    left: ((heroBox[0] - viewX) / vw) * width,
    top,
    width: heroWidth,
    height: heroHeight,
    backgroundPosition: vw < 1 ? viewX / (1 - vw) : 0,
    backgroundPositionY: vh < 1 ? 1 : 0,
    glass: {
      x: ((glass[0] - viewX) / vw) * width,
      y: ((glass[1] - viewY) / vh) * height,
      rx: (glass[2] / vw) * width,
      ry: (glass[3] / vh) * height,
    },
    light: {
      left: ((contactX - viewX - 0.08) / vw) * width,
      top: ((handY - viewY - 0.1422224) / vh) * height,
      width: (0.16 / vw) * width,
      height: (0.2844448 / vh) * height,
    },
    view: [viewX, viewY, vw, vh],
    heroBox,
  };
}

export function mirrorSeaLayout(
  width: number,
  height: number,
  landscape: boolean,
  polished: boolean,
  appearance = false,
) {
  if (!landscape) {
    const [x, y, w, h] = mirrorSea.portraitHero;
    const [cx, cy, cw, ch] = mirrorSea.portraitCrop;
    return {
      view: polished ? portraitView(mirrorSea.portraitCrop, 'mirrorsea') : mirrorSea.portraitCrop,
      heroBox: [cx + x * cw, cy + y * ch, w * cw, h * ch],
    };
  }
  if (appearance) return mirrorSeaAppearancePlacement(width, height);
  const vw = Math.min(1, width / height / originalAspect);
  const vh = Math.min(1, originalAspect / (width / height));
  return { view: [(1 - vw) * mirrorSea.cropPosition, 0, vw, vh], heroBox: mirrorSea.landscapeHero };
}
