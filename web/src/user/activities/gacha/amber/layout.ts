import { amber } from './config';
import { portraitView } from '../portrait';

export function amberAppearancePlacement(width: number, height: number) {
  const desktop = width >= 1024;
  const heroHeight = height * (desktop ? 0.72 : 0.75);
  const heroWidth = (heroHeight * 1037) / 1516;
  const center = desktop ? width - Math.max(64, Math.min(180, (width - 1184) / 4)) : width * 0.62;
  return {
    left: center - heroWidth / 2,
    top: height * 0.965 - heroHeight,
    width: heroWidth,
    height: heroHeight,
  };
}

export function amberLayout(
  width: number,
  height: number,
  landscape: boolean,
  polished: boolean,
  appearance = false,
) {
  if (!landscape) {
    const view = polished ? portraitView(amber.portraitCrop, 'amber') : amber.portraitCrop;
    const [x, y, w, h] = amber.portraitHero;
    const [cx, cy, cw, ch] = amber.portraitCrop;
    return { view, heroBox: [cx + x * cw, cy + y * ch, w * cw, h * ch] };
  }
  const aspect = width / height;
  const original = 1672 / 941;
  const vw = Math.min(1, aspect / original);
  const vh = Math.min(1, original / aspect);
  const view = [(1 - vw) * (polished ? 0.82 : amber.cropPosition), 0, vw, vh];
  if (!appearance) return { view, heroBox: amber.landscapeHero };

  // A full-page backdrop keeps the face in the side margin independently of the hall crop.
  const box = amberAppearancePlacement(width, height);
  return {
    view,
    heroBox: [
      view[0] + (box.left / width) * vw,
      (box.top / height) * vh,
      (box.width / width) * vw,
      (box.height / height) * vh,
    ],
  };
}
