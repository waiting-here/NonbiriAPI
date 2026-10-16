import { useEffect, useRef, useState } from 'react';
import type { SpecialTheme } from '../activities/gacha/special-scenes';
import { artSourceSet } from '../activities/gacha/art-resolution';

const settings = () => ({
  dark: document.documentElement.dataset.theme === 'dark',
  high: Math.max(innerWidth, innerHeight) * (devicePixelRatio || 1) > 1920,
});

export function SpecialBackdrop({ theme }: { theme: SpecialTheme }) {
  const canvas = useRef<HTMLCanvasElement>(null);
  const [quality, setQuality] = useState(settings);
  useEffect(() => {
    const update = () => {
      const next = settings();
      setQuality((current) =>
        current.dark === next.dark && current.high === next.high ? current : next,
      );
    };
    const observer = new MutationObserver(update);
    observer.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ['data-theme'],
    });
    window.addEventListener('resize', update);
    return () => {
      observer.disconnect();
      window.removeEventListener('resize', update);
    };
  }, []);
  useEffect(() => {
    const surface = canvas.current;
    if (!surface) return;
    const reduced = matchMedia('(prefers-reduced-motion: reduce)');
    let controller: AbortController | undefined;
    let release: (() => void) | undefined;
    const update = () => {
      controller?.abort();
      release?.();
      release = undefined;
      surface.dataset.ready = 'false';
      if (reduced.matches) return;
      const next = new AbortController();
      controller = next;
      void import('../activities/gacha/special-renderer')
        .then(({ renderSpecial }) =>
          renderSpecial(surface, theme, {
            landscape: true,
            dark: quality.dark,
            highResolution: quality.high,
            signal: next.signal,
          }),
        )
        .then((stop) => {
          if (next.signal.aborted) stop();
          else release = stop;
        })
        .catch(() => {
          /* Keep the matching static painting. */
        });
    };
    reduced.addEventListener('change', update);
    update();
    return () => {
      reduced.removeEventListener('change', update);
      controller?.abort();
      release?.();
    };
  }, [theme, quality]);
  const src =
    '/assets/gacha/appearances/' + theme + '-' + (quality.dark ? 'dark' : 'light') + '.webp';
  return (
    <>
      <img
        className="nb-appearance-still"
        data-scene={theme}
        src={src}
        srcSet={artSourceSet(src)}
        sizes="(max-aspect-ratio: 16/9) 177.78vh, 100vw"
        alt=""
        aria-hidden="true"
      />
      <canvas
        ref={canvas}
        className="nb-appearance-backdrop"
        data-ready="false"
        aria-hidden="true"
      />
    </>
  );
}
