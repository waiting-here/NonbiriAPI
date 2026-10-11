import { useEffect, useRef, useState } from 'react';
import type { WallpaperQuality, WallpaperTheme } from './wallpaper-motion';

const colorMode = (): WallpaperTheme =>
  document.documentElement.dataset.theme === 'dark' ? 'dark' : 'light';
const qualityLevel = (): WallpaperQuality =>
  Math.max(window.innerWidth, window.innerHeight) * (window.devicePixelRatio || 1) > 1920
    ? 'high'
    : 'standard';

export function AppearanceBackdrop({ selected }: { selected: string }) {
  const canvas = useRef<HTMLCanvasElement>(null);
  const [theme, setTheme] = useState(colorMode);
  const [quality, setQuality] = useState(qualityLevel);
  useEffect(() => {
    const observer = new MutationObserver(() => setTheme(colorMode()));
    observer.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ['data-theme'],
    });
    const resize = () => setQuality(qualityLevel());
    window.addEventListener('resize', resize);
    return () => {
      observer.disconnect();
      window.removeEventListener('resize', resize);
    };
  }, []);
  useEffect(() => {
    const surface = canvas.current;
    if (!surface || (selected !== 'celestial' && selected !== 'wandering')) return;
    const reduced = window.matchMedia('(prefers-reduced-motion: reduce)');
    let controller: AbortController | undefined, release: (() => void) | undefined;
    const update = () => {
      controller?.abort();
      release?.();
      release = undefined;
      surface.dataset.ready = 'false';
      if (reduced.matches) return;
      const next = new AbortController();
      controller = next;
      void import('./wallpaper-motion')
        .then(({ renderWallpaper }) =>
          renderWallpaper(surface, selected, theme, next.signal, quality),
        )
        .then((stop) => {
          if (next.signal.aborted) stop();
          else release = stop;
        })
        .catch(() => {
          /* The full static painting remains visible. */
        });
    };
    reduced.addEventListener('change', update);
    update();
    return () => {
      reduced.removeEventListener('change', update);
      controller?.abort();
      release?.();
    };
  }, [selected, theme, quality]);
  if (selected !== 'celestial' && selected !== 'wandering') return null;
  const revision = selected === 'celestial' || theme === 'light' ? '?v=5' : '';
  const filename = selected + '-' + theme + '.webp' + revision;
  return (
    <>
      <img
        className="nb-appearance-still"
        alt=""
        aria-hidden="true"
        src={'/assets/gacha/appearances/' + filename}
        srcSet={
          '/assets/gacha/appearances/' +
          filename +
          ' 1672w, /assets/gacha/hires/appearances/' +
          filename +
          ' 3840w'
        }
        sizes="(max-aspect-ratio: 16/9) 177.78vh, 100vw"
      />
      <canvas
        ref={canvas}
        className="nb-appearance-backdrop"
        aria-hidden="true"
        data-ready="false"
      />
    </>
  );
}
