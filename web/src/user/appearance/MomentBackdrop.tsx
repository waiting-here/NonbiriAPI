import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { artSourceSet } from '../activities/gacha/art-resolution';
import { isRare } from '../activities/gacha/catalog';
import {
  momentFrameStyle,
  momentImageSizes,
  momentHorizontalMargins,
} from '../activities/gacha/moment-frame';
import { useMomentExpansion } from '../activities/gacha/moment-registration';
import { viewportMomentLayout } from '../activities/gacha/viewport-moment';
import { defaultMoment, momentByKey, momentArtworkKeys } from './moments';
import { useArtworkPolicy } from '@shared/artwork/context';
import { useArtworkHue } from './artwork-hue';
import './moments.css';

export function MomentBackdrop({ moment }: { moment?: string }) {
  const policy = useArtworkPolicy();
  const preferred = momentByKey.get(moment ?? '') ?? defaultMoment;
  const artwork = policy.allowed(momentArtworkKeys(preferred)) ? preferred : defaultMoment;
  return <PermittedMomentBackdrop artwork={artwork} />;
}
function PermittedMomentBackdrop({ artwork }: { artwork: typeof defaultMoment }) {
  const card = artwork.card;
  const rare = !!card && isRare(card.rarity);
  const {
    expansion,
    scene: expandedScene,
    ready: registrationReady,
  } = useMomentExpansion(card?.id);
  const [viewportSize, setViewportSize] = useState(() => [window.innerWidth, window.innerHeight]);
  const fullScene = expandedScene?.viewport;
  const hasFullScene = !!fullScene;
  const viewport = useMemo(
    () =>
      expandedScene?.viewport
        ? viewportMomentLayout(expandedScene, viewportSize[0], viewportSize[1])
        : undefined,
    [expandedScene, viewportSize],
  );
  useLayoutEffect(() => {
    if (!hasFullScene) return;
    const measure = () =>
      setViewportSize((current) =>
        current[0] === window.innerWidth && current[1] === window.innerHeight
          ? current
          : [window.innerWidth, window.innerHeight],
      );
    measure();
    window.addEventListener('resize', measure);
    return () => window.removeEventListener('resize', measure);
  }, [hasFullScene]);
  const expandedStyle = expansion ? momentFrameStyle(expansion) : undefined;
  const asset = (path: string) => '/assets/gacha/' + path;
  const expandedHero = expansion?.hero ? asset(expansion.hero) : undefined;
  const characterSizes = viewport
    ? viewport.character.width + 'px'
    : '(width < 40rem) 100vw, 66.67vh';
  const heroSizes = expandedHero
    ? viewport
      ? (viewport.character.width * expansion!.canvas[0]) / expansion!.originalRegion.width + 'px'
      : momentImageSizes(expansion!)
    : characterSizes;
  const canvas = useRef<HTMLCanvasElement>(null);
  const scene = useRef<HTMLImageElement>(null);
  useArtworkHue(scene, `${artwork.key}:${expandedScene?.path ?? ''}:${registrationReady}`);
  const hero =
    expandedHero ??
    (card
      ? rare
        ? '/assets/gacha/motion/' + card.id + '/hero.webp'
        : '/assets/gacha/layers/' + card.id + '.webp'
      : undefined);
  const expandedBackground = expandedScene ? asset(expandedScene.path) : undefined;
  useLayoutEffect(() => {
    const root = document.documentElement;
    if (viewport) {
      root.style.setProperty('--nb-moment-gutter', viewport.reserve + 'px');
      root.style.setProperty('--nb-moment-extra-reserve', '0px');
      root.style.setProperty('--nb-moment-right-reserve', '0px');
    } else if (expansion) {
      const margins = momentHorizontalMargins(expansion);
      root.style.setProperty('--nb-moment-width-ratio', String(1 + margins.total));
      root.style.setProperty(
        '--nb-moment-height-ratio',
        String(
          (expansion.canvas[1] - (expansion.contentTop ?? 0)) / expansion.originalRegion.height,
        ),
      );
      root.style.setProperty(
        '--nb-moment-extra-reserve',
        'calc(var(--nb-moment-gutter) * ' + margins.total + ')',
      );
      root.style.setProperty(
        '--nb-moment-right-reserve',
        'calc(var(--nb-moment-gutter) * ' + margins.right + ')',
      );
    }
    return () => {
      root.style.removeProperty('--nb-moment-width-ratio');
      root.style.removeProperty('--nb-moment-height-ratio');
      root.style.removeProperty('--nb-moment-extra-reserve');
      root.style.removeProperty('--nb-moment-right-reserve');
      root.style.removeProperty('--nb-moment-gutter');
    };
  }, [expansion, viewport]);
  useEffect(() => {
    const surface = canvas.current;
    if (!surface || !card || !rare || !registrationReady) return;
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
        surface.getBoundingClientRect().width * (window.devicePixelRatio || 1) >
        (expansion?.canvas[0] ?? 1024);
      void import('../activities/gacha/ssr-renderer')
        .then(({ renderSSR }) =>
          renderSSR(surface, card.id, undefined, undefined, highResolution, {
            characterOnly: true,
            momentFrame: expansion,
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
        surface.getBoundingClientRect().width * (window.devicePixelRatio || 1) >
          (expansion?.canvas[0] ?? 1024) !==
          highResolution
      )
        update();
    };
    const observer = typeof ResizeObserver === 'undefined' ? undefined : new ResizeObserver(resize);
    observer?.observe(surface);
    window.addEventListener('resize', resize);
    reduced.addEventListener('change', update);
    update();
    return () => {
      window.removeEventListener('resize', resize);
      observer?.disconnect();
      reduced.removeEventListener('change', update);
      controller?.abort();
      release?.();
    };
  }, [card, rare, expansion, registrationReady]);
  const background =
    expandedBackground ??
    (card ? '/assets/gacha/backgrounds/' + card.theme + '.webp' : artwork.art);
  if (rare && !registrationReady) return null;
  return (
    <div
      className="nb-moment-backdrop"
      aria-hidden="true"
      data-moment={artwork.key}
      data-registered={!!expandedScene}
      data-expanded-scene={!!expandedScene}
      data-viewport={!!viewport}
    >
      <img
        ref={expandedScene && !viewport ? undefined : scene}
        className="nb-moment-backdrop__scene"
        style={
          viewport ? { ...viewport.background, position: 'absolute', maxWidth: 'none' } : undefined
        }
        src={background}
        srcSet={
          expandedScene
            ? artSourceSet(background, expandedScene.canvas[0], expandedScene.highResolutionWidth)
            : artSourceSet(background)
        }
        sizes={
          viewport ? viewport.background.width + 'px' : '(max-aspect-ratio: 16/9) 177.78vh, 100vw'
        }
        alt=""
      />
      {hero && (
        <div
          className="nb-moment-character"
          data-expanded={Boolean(expansion || expandedScene)}
          style={viewport ? viewport.character : undefined}
        >
          {expandedScene && expandedBackground && !viewport && (
            <img
              ref={scene}
              className="nb-moment-character__scene"
              style={momentFrameStyle(expandedScene)}
              src={expandedBackground}
              srcSet={artSourceSet(
                expandedBackground,
                expandedScene.canvas[0],
                expandedScene.highResolutionWidth ?? expandedScene.canvas[0] * 2.5,
              )}
              sizes={
                'max(100vw, ' + (100 * expandedScene.canvas[0]) / expandedScene.canvas[1] + 'dvh)'
              }
              alt=""
            />
          )}
          {fullScene?.shadow && (
            <img
              className="nb-moment-character__shadow"
              src={asset(fullScene.shadow)}
              srcSet={artSourceSet(asset(fullScene.shadow), 1024, 2560)}
              sizes={characterSizes}
              alt=""
            />
          )}
          <img
            className="nb-moment-character__hero"
            style={expandedHero ? expandedStyle : undefined}
            src={hero}
            srcSet={artSourceSet(
              hero,
              expandedHero ? expansion!.canvas[0] : 1024,
              expandedHero ? (expansion!.highResolutionWidth ?? expansion!.canvas[0] * 2.5) : 2560,
            )}
            sizes={heroSizes}
            alt=""
          />
          {rare && (
            <canvas
              style={expandedStyle}
              ref={canvas}
              width="768"
              height="1152"
              data-ready="false"
            />
          )}
        </div>
      )}
    </div>
  );
}
