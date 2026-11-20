import { useEffect, useRef } from 'react';
import type { SpecialTheme } from '../activities/gacha/special-scenes';
import { TomorrowArtwork } from '../activities/gacha/tomorrow/Artwork';
import { AmberStill } from './AmberStill';
import { RomanceStill } from './RomanceStill';
import { MirrorSeaStill } from './MirrorSeaStill';
import { useArtworkPolicy } from '@shared/artwork/context';

import { startBackground } from './start-background';
import { useBackgroundPlayback } from './playback-context';

export function SpecialBackdrop({
  theme,
  dark,
  high,
}: {
  theme: SpecialTheme;
  dark: boolean;
  high: boolean;
}) {
  const canvas = useRef<HTMLCanvasElement>(null);
  const { playback, animated } = useBackgroundPlayback();
  const policy = useArtworkPolicy();
  const allowed = policy.allowed('appearance:' + theme);
  useEffect(() => {
    const surface = canvas.current;
    if (!surface || !animated || !allowed) return;
    return startBackground(surface, playback, async (signal) => {
      const { renderSpecial } = await import('../activities/gacha/special-renderer');
      return renderSpecial(surface, theme, {
        landscape: true,
        variant: 'appearance',
        dark,
        highResolution: high,
        signal,
        ambient: true,
        playback,
      });
    });
  }, [theme, dark, high, playback, animated, allowed]);
  if (!allowed) return null;
  return (
    <>
      {theme === 'tomorrow' ? (
        <TomorrowArtwork
          walking
          variant="appearance"
          dark={dark}
          animated={false}
          className="nb-tomorrow-still"
        />
      ) : theme === 'amber' ? (
        <AmberStill dark={dark} />
      ) : theme === 'elopement' || theme === 'heart-thief' ? (
        <RomanceStill theme={theme} dark={dark} />
      ) : (
        <MirrorSeaStill dark={dark} />
      )}
      <canvas
        key={playback.sceneKey}
        ref={canvas}
        className="nb-appearance-backdrop"
        data-ready="false"
        aria-hidden="true"
      />
    </>
  );
}
