import { useEffect, useState } from 'react';
import { artSourceSet } from '../activities/gacha/art-resolution';
import { heartThiefStance } from '../activities/gacha/romance/heart-thief-stance';
import {
  romanceAppearancePlacement,
  romanceBackdropPlacement,
  type RomanceTheme,
} from '../activities/gacha/romance/layout';

export function RomanceStill({ theme, dark }: { theme: RomanceTheme; dark: boolean }) {
  const [size, setSize] = useState(() => [innerWidth, innerHeight]);
  useEffect(() => {
    const resize = () => setSize([innerWidth, innerHeight]);
    window.addEventListener('resize', resize);
    return () => window.removeEventListener('resize', resize);
  }, []);
  const mode = dark ? 'dark' : 'light';
  const id = theme === 'elopement' ? 'p-claude' : 'q-glm';
  const src = `/assets/gacha/appearances/${theme}-${mode}.webp`;
  const hero = `/assets/gacha/motion/${id}/appearance-hero-${mode}.webp`;
  const box = romanceAppearancePlacement(size[0], size[1], theme);
  const background = romanceBackdropPlacement(size[0], size[1], size[0], theme);
  return (
    <>
      <img
        className="nb-appearance-still"
        data-scene={theme}
        src={src}
        srcSet={artSourceSet(src, theme === 'heart-thief' ? 1536 : 1672)}
        sizes={
          theme === 'heart-thief'
            ? '(max-aspect-ratio: 3/2) 150vh, 106vw'
            : '(max-aspect-ratio: 16/9) 177.78vh, 100vw'
        }
        style={
          theme === 'heart-thief'
            ? {
                left: background.left,
                top: background.top,
                width: 1536 * background.scale,
                height: 1024 * background.scale,
                maxWidth: 'none',
                objectFit: 'fill',
              }
            : { objectPosition: size[0] < 768 ? '48% top' : '78% top' }
        }
        alt=""
        aria-hidden="true"
      />
      <img
        className="nb-romance-still-character"
        src={hero}
        alt=""
        aria-hidden="true"
        style={{
          left: box.left,
          top: box.top,
          width: box.width,
          height:
            box.height *
            (theme === 'heart-thief'
              ? heartThiefStance.plateHeight / heartThiefStance.contentHeight
              : 1),
        }}
      />
    </>
  );
}
