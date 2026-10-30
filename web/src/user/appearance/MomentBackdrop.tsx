import { useEffect, useRef } from 'react';
import { artSourceSet } from '../activities/gacha/art-resolution';
import { isRare } from '../activities/gacha/catalog';
import {
  momentFrameStyle,
  momentImageSizes,
  momentHorizontalMargins,
} from '../activities/gacha/moment-frame';
import { useMomentExpansion } from '../activities/gacha/moment-registration';
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
  const expandedStyle = expansion ? momentFrameStyle(expansion) : undefined;
  const asset = (path: string) => '/assets/gacha/' + path;
  const expandedHero = expansion?.hero ? asset(expansion.hero) : undefined;
  const canvas = useRef<HTMLCanvasElement>(null);
  const scene = useRef<HTMLImageElement>(null);
  useArtworkHue(scene, artwork.key);
  const hero =
    expandedHero ??
    (card
      ? rare
        ? '/assets/gacha/motion/' + card.id + '/hero.webp'
        : '/assets/gacha/layers/' + card.id + '.webp'
      : undefined);
  const expandedBackground = expandedScene ? asset(expandedScene.path) : undefined;
  const sceneFrameWidth = expandedScene
    ? 'max(' +
      (100 * expandedScene.originalRegion.width) / expandedScene.canvas[0] +
      'vw, ' +
      (100 * expandedScene.originalRegion.width) / expandedScene.canvas[1] +
      'dvh)'
    : undefined;
  const registeredScene =
    !expandedScene && card?.theme === 's'
      ? '/assets/gacha/motion/' + card.id + '/background.webp'
      : undefined;
  useEffect(() => {
    const root = document.documentElement;
    if (expansion) {
      const margins = momentHorizontalMargins(expansion);
      root.style.setProperty(
        '--nb-moment-extra-reserve',
        'calc(var(--nb-moment-gutter) * ' + margins.total + ')',
      );
      root.style.setProperty(
        '--nb-moment-right-reserve',
        'calc(var(--nb-moment-gutter) * ' + margins.right + ')',
      );
    }
    if (sceneFrameWidth) root.style.setProperty('--nb-moment-scene-frame-width', sceneFrameWidth);
    return () => {
      root.style.removeProperty('--nb-moment-extra-reserve');
      root.style.removeProperty('--nb-moment-right-reserve');
      root.style.removeProperty('--nb-moment-scene-frame-width');
    };
  }, [expansion, sceneFrameWidth]);
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
    window.addEventListener('resize', resize);
    reduced.addEventListener('change', update);
    update();
    return () => {
      window.removeEventListener('resize', resize);
      reduced.removeEventListener('change', update);
      controller?.abort();
      release?.();
    };
  }, [card, rare, expansion, registrationReady]);
  const background =
    registeredScene ?? (card ? '/assets/gacha/backgrounds/' + card.theme + '.webp' : artwork.art);
  return (
    <div
      className="nb-moment-backdrop"
      aria-hidden="true"
      data-moment={artwork.key}
      data-registered={!!registeredScene}
      data-expanded-scene={!!expandedScene}
    >
      <img
        ref={expandedScene ? undefined : scene}
        className="nb-moment-backdrop__scene"
        src={background}
        srcSet={registeredScene ? artSourceSet(background, 1024, 2560) : artSourceSet(background)}
        sizes={
          registeredScene
            ? '(max-aspect-ratio: 2/3) 66.67vh, 100vw'
            : '(max-aspect-ratio: 16/9) 177.78vh, 100vw'
        }
        alt=""
      />
      {hero && (
        <div className="nb-moment-character" data-expanded={Boolean(expansion || expandedScene)}>
          {expandedScene && expandedBackground && (
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
          {registeredScene && (
            <img
              className="nb-moment-character__plate"
              src={registeredScene}
              srcSet={artSourceSet(registeredScene, 1024, 2560)}
              sizes="(width < 40rem) 100vw, 66.67vh"
              alt=""
            />
          )}
          <img
            style={expandedHero ? expandedStyle : undefined}
            src={hero}
            srcSet={artSourceSet(
              hero,
              expandedHero ? expansion!.canvas[0] : 1024,
              expandedHero ? (expansion!.highResolutionWidth ?? expansion!.canvas[0] * 2.5) : 2560,
            )}
            sizes={
              sceneFrameWidth ??
              (expandedHero ? momentImageSizes(expansion!) : '(width < 40rem) 100vw, 66.67vh')
            }
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
