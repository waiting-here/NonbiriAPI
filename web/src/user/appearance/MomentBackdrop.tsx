import { useEffect, useRef } from 'react';
import { artSourceSet } from '../activities/gacha/art-resolution';
import { isRare } from '../activities/gacha/catalog';
import { defaultMoment, momentByKey } from './moments';
import './moments.css';

export function MomentBackdrop({ moment }: { moment?: string }) {
  const artwork = momentByKey.get(moment ?? '') ?? defaultMoment;
  const card = artwork.card;
  const rare = !!card && isRare(card.rarity);
  const canvas = useRef<HTMLCanvasElement>(null);
  const hero = card
    ? rare
      ? '/assets/gacha/motion/' + card.id + '/hero.webp'
      : '/assets/gacha/layers/' + card.id + '.webp'
    : undefined;
  useEffect(() => {
    const surface = canvas.current;
    if (!surface || !card || !rare) return;
    const reduced = window.matchMedia('(prefers-reduced-motion: reduce)');
    let controller: AbortController | undefined;
    let release: (() => void) | undefined;
    let highResolution = false;
    const update = () => {
      controller?.abort();
      release?.();
      release = undefined;
      surface.dataset.ready = 'false';
      if (reduced.matches) return;
      const next = new AbortController();
      controller = next;
      highResolution =
        surface.getBoundingClientRect().width * (window.devicePixelRatio || 1) > 1024;
      void import('../activities/gacha/ssr-renderer')
        .then(({ renderSSR }) =>
          renderSSR(surface, card.id, undefined, undefined, highResolution, {
            characterOnly: true,
            signal: next.signal,
          }),
        )
        .then((stop) => {
          if (next.signal.aborted) stop();
          else {
            release = stop;
            surface.dataset.ready = 'true';
          }
        })
        .catch(() => {
          /* The transparent static character remains visible. */
        });
    };
    const resize = () => {
      if (
        !reduced.matches &&
        surface.getBoundingClientRect().width * (window.devicePixelRatio || 1) > 1024 !==
          highResolution
      )
        update();
    };
    window.addEventListener('resize', resize);
    reduced.addEventListener('change', update);
    update();
    return () => {
      window.removeEventListener('resize', resize);
      reduced.removeEventListener('change', update);
      controller?.abort();
      release?.();
    };
  }, [card, rare]);
  const background = card ? '/assets/gacha/backgrounds/' + card.theme + '.webp' : artwork.art;
  return (
    <div className="nb-moment-backdrop" aria-hidden="true" data-moment={artwork.key}>
      <img
        className="nb-moment-backdrop__scene"
        src={background}
        srcSet={artSourceSet(background)}
        sizes="(max-aspect-ratio: 16/9) 177.78vh, 100vw"
        alt=""
      />
      {hero && (
        <div className="nb-moment-character">
          <img
            src={hero}
            srcSet={artSourceSet(hero, 1024, 2560)}
            sizes="(width < 40rem) 100vw, 66.67vh"
            alt=""
          />
          {rare && <canvas ref={canvas} width="768" height="1152" data-ready="false" />}
        </div>
      )}
    </div>
  );
}
