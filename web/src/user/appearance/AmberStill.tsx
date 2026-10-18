import { useEffect, useState } from 'react';
import { artSourceSet } from '../activities/gacha/art-resolution';
import { amberAppearancePlacement } from '../activities/gacha/amber/layout';

export function AmberStill({ dark }: { dark: boolean }) {
  const [size, setSize] = useState(() => [innerWidth, innerHeight]);
  useEffect(() => {
    const resize = () => setSize([innerWidth, innerHeight]);
    window.addEventListener('resize', resize);
    return () => window.removeEventListener('resize', resize);
  }, []);
  const mode = dark ? 'dark' : 'light';
  const src = '/assets/gacha/appearances/amber-' + mode + '.webp';
  const box = amberAppearancePlacement(size[0], size[1]);
  return (
    <>
      <img
        className="nb-appearance-still"
        data-scene="amber"
        src={src}
        srcSet={artSourceSet(src)}
        sizes="(max-aspect-ratio: 16/9) 177.78vh, 100vw"
        alt=""
        aria-hidden="true"
      />
      <img
        className="nb-amber-still-character"
        alt=""
        aria-hidden="true"
        src={'/assets/gacha/motion/o-claude/appearance-hero-' + mode + '.webp'}
        style={{
          left: box.left,
          top: box.top,
          width: box.width,
          height: (box.height * 1560) / 1516,
        }}
      />
    </>
  );
}
