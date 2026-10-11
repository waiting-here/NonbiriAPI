import { useEffect, useRef } from 'react';

export function AppearanceBackdrop({ selected }: { selected: string }) {
  const canvas = useRef<HTMLCanvasElement>(null);
  useEffect(() => {
    const surface = canvas.current;
    if (!surface || (selected !== 'celestial' && selected !== 'wandering')) return;
    const reduced = window.matchMedia('(prefers-reduced-motion: reduce)');
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
      const theme = document.documentElement.dataset.theme === 'dark' ? 'dark' : 'light';
      void import('./wallpaper-motion')
        .then(({ renderWallpaper }) => renderWallpaper(surface, selected, theme, next.signal))
        .then((stop) => {
          if (next.signal.aborted) stop();
          else release = stop;
        })
        .catch(() => {
          // The original painting remains visible if animation is unavailable.
        });
    };
    const observer = new MutationObserver(update);
    observer.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ['data-theme'],
    });
    reduced.addEventListener('change', update);
    update();
    return () => {
      observer.disconnect();
      reduced.removeEventListener('change', update);
      controller?.abort();
      release?.();
    };
  }, [selected]);
  return selected === 'default' ? null : (
    <canvas ref={canvas} className="nb-appearance-backdrop" aria-hidden="true" data-ready="false" />
  );
}
