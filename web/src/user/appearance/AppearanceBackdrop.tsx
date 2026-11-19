import { useEffect, useRef, useState } from 'react';
import { MomentBackdrop } from './MomentBackdrop';
import { SpecialBackdrop } from './SpecialBackdrop';
import { isSpecialTheme } from '../activities/gacha/special-scenes';
import { artSourceSet, highResolutionArt } from '../activities/gacha/art-resolution';
import type { WallpaperTheme } from './wallpaper-motion';
import { useArtworkPolicy } from '@shared/artwork/context';
import { useBackgroundPlayback } from './playback-context';
import { ArtworkStage } from './stage';
import { startBackground } from './start-background';
import { useStageSize } from './stage-size';
import { defaultMoment, momentByKey, momentArtworkKeys } from './moments';
import { eligibleMoment, useOptionalAppearance } from './context';

const colorMode = (): WallpaperTheme =>
  document.documentElement.dataset.theme === 'dark' ? 'dark' : 'light';

export function ActiveAppearanceBackdrop() {
  const appearance = useOptionalAppearance();
  const state = appearance?.state;
  return state?.unlocked.includes(state.selected) ? (
    <AppearanceBackdrop selected={state.selected} moment={eligibleMoment(state)} />
  ) : null;
}

export function AppearanceBackdrop({ selected, moment }: { selected: string; moment?: string }) {
  const policy = useArtworkPolicy();
  if (selected === 'default' || !policy.allowed('appearance:' + selected)) return null;
  const choice = momentByKey.get(moment ?? '') ?? defaultMoment;
  const artwork = policy.allowed(momentArtworkKeys(choice)) ? choice : defaultMoment;
  return (
    <ArtworkStage group={selected === 'heart-moment' && !artwork.card}>
      {selected === 'heart-moment' ? (
        <MomentBackdrop key={artwork.key} moment={artwork.key} />
      ) : (
        <Wallpaper key={selected} selected={selected} />
      )}
    </ArtworkStage>
  );
}

function Wallpaper({ selected }: { selected: string }) {
  const canvas = useRef<HTMLCanvasElement>(null);
  const { playback, animated } = useBackgroundPlayback();
  const [theme, setTheme] = useState(colorMode);
  const size = useStageSize();
  const high = playback.highest || Math.max(...size) * (window.devicePixelRatio || 1) > 1920;
  useEffect(() => {
    const observer = new MutationObserver(() => setTheme(colorMode()));
    observer.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ['data-theme'],
    });
    return () => observer.disconnect();
  }, []);
  useEffect(() => {
    const surface = canvas.current;
    if (!surface || !animated || (selected !== 'celestial' && selected !== 'wandering')) return;
    return startBackground(surface, playback, async (signal) => {
      const { renderWallpaper } = await import('./wallpaper-motion');
      return renderWallpaper(
        surface,
        selected,
        theme,
        signal,
        high ? 'high' : 'standard',
        playback,
      );
    });
  }, [selected, theme, high, playback, animated]);
  if (isSpecialTheme(selected))
    return <SpecialBackdrop theme={selected} dark={theme === 'dark'} high={high} />;
  if (selected !== 'celestial' && selected !== 'wandering') return null;
  const src = '/assets/gacha/appearances/' + selected + '-' + theme + '.webp?v=7';
  return (
    <>
      <img
        className="nb-appearance-still"
        alt=""
        src={playback.highest ? highResolutionArt(src) : src}
        srcSet={playback.highest ? undefined : artSourceSet(src)}
        sizes={Math.max(size[0], (size[1] * 16) / 9) + 'px'}
      />
      <canvas
        key={playback.sceneKey}
        ref={canvas}
        className="nb-appearance-backdrop"
        aria-hidden="true"
        data-ready="false"
      />
    </>
  );
}
