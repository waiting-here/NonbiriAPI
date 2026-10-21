import { useEffect, useState } from 'react';
import { artSourceSet } from '../activities/gacha/art-resolution';
import { mirrorSeaAppearancePlacement } from '../activities/gacha/mirror-sea-layout';

export function MirrorSeaStill({ dark }: { dark: boolean }) {
  const [size, setSize] = useState(() => [innerWidth, innerHeight]);
  useEffect(() => {
    const resize = () => setSize([innerWidth, innerHeight]);
    window.addEventListener('resize', resize);
    return () => window.removeEventListener('resize', resize);
  }, []);
  const mode = dark ? 'dark' : 'light';
  const background = `/assets/gacha/appearances/mirrorsea-${mode}.webp`;
  const root = '/assets/gacha/motion/n-gemini/';
  const box = mirrorSeaAppearancePlacement(size[0], size[1]);
  const characterStyle = { left: box.left, top: box.top, width: box.width, height: box.height };
  const mask = `radial-gradient(ellipse ${box.glass.rx}px ${box.glass.ry}px at ${box.glass.x}px ${box.glass.y}px, #000 89%, rgba(0,0,0,.84) 91.4%, rgba(0,0,0,.5) 93.75%, rgba(0,0,0,.16) 96.1%, transparent 98.5%)`;
  return (
    <>
      <img
        className="nb-appearance-still"
        data-scene="mirrorsea"
        src={background}
        srcSet={artSourceSet(background)}
        sizes="(max-aspect-ratio: 16/9) 177.78vh, 100vw"
        style={{
          objectPosition: `${box.backgroundPosition * 100}% ${box.backgroundPositionY * 100}%`,
        }}
        alt=""
        aria-hidden="true"
      />
      <div className="nb-mirror-still-reflection" style={{ maskImage: mask }} aria-hidden="true">
        <img
          src={root + 'appearance-reflection.webp'}
          srcSet={artSourceSet(root + 'appearance-reflection.webp', 1024, 2560)}
          sizes={`${box.width}px`}
          style={characterStyle}
          alt=""
        />
      </div>
      <img
        className="nb-mirror-still-light"
        src={root + 'appearance-light.webp'}
        srcSet={artSourceSet(root + 'appearance-light.webp', 512, 1024)}
        sizes={`${box.light.width}px`}
        style={box.light}
        alt=""
        aria-hidden="true"
      />
      <img
        className="nb-mirror-still-character"
        src={root + `appearance-hero-${mode}.webp`}
        srcSet={artSourceSet(root + `appearance-hero-${mode}.webp`, 1024, 2560)}
        sizes={`${box.width}px`}
        style={characterStyle}
        alt=""
        aria-hidden="true"
      />
    </>
  );
}
