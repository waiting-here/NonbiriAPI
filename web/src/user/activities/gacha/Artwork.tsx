import { useEffect, useRef, useState } from 'react';
import { isRare } from './catalog';
import type { Card } from './types';
import { renderSSR } from './ssr-renderer';
import { artSourceSet } from './art-resolution';

export function Artwork({
  card,
  animated = false,
  full = false,
  paused = false,
}: {
  card: Card;
  animated?: boolean;
  full?: boolean;
  paused?: boolean;
}) {
  const canvas = useRef<HTMLCanvasElement>(null);
  const [ready, setReady] = useState(false);
  const [highResolution, setHighResolution] = useState(
    () => ((window.innerHeight * 0.8) / 1.5) * (window.devicePixelRatio || 1) > 1024,
  );
  useEffect(() => {
    const surface = canvas.current;
    if (!surface) return;
    const measure = () =>
      setHighResolution(
        surface.getBoundingClientRect().width * (window.devicePixelRatio || 1) > 1024,
      );
    const observer = new ResizeObserver(measure);
    observer.observe(surface);
    window.addEventListener('resize', measure);
    measure();
    return () => {
      observer.disconnect();
      window.removeEventListener('resize', measure);
    };
  }, [animated, card.id, card.rarity]);
  const pausedRef = useRef(paused);
  useEffect(() => {
    pausedRef.current = paused;
  }, [paused]);
  useEffect(() => {
    if (!animated || !isRare(card.rarity) || !canvas.current) return;
    let disposed = false,
      cleanup: (() => void) | undefined;
    void renderSSR(canvas.current, card.id, undefined, () => pausedRef.current, highResolution)
      .then((dispose) => {
        if (disposed) dispose();
        else {
          cleanup = dispose;
          setReady(true);
        }
      })
      .catch(() => {
        /* The complete static illustration remains visible. */
      });
    return () => {
      disposed = true;
      cleanup?.();
      setReady(false);
    };
  }, [animated, card.id, card.rarity, highResolution]);
  return (
    <span className="gacha-art">
      <img
        src={full ? card.art : card.thumb}
        srcSet={full ? artSourceSet(card.art, 1024, 2560) : undefined}
        sizes={full ? '(width < 40rem) 90vw, 56vh' : undefined}
        alt={card.name}
        loading={full ? 'eager' : 'lazy'}
        width="1024"
        height="1536"
      />
      {animated && isRare(card.rarity) && (
        <canvas
          ref={canvas}
          width="768"
          height="1152"
          className={ready ? 'is-ready' : ''}
          aria-hidden="true"
        />
      )}
    </span>
  );
}
