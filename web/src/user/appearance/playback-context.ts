import { createContext, useContext, useSyncExternalStore } from 'react';
import { ArtworkPlayback } from '@shared/animation/artwork-playback';

export type Motion = 'system' | 'full' | 'static';
export type Quality = 'auto' | 'highest';

const reducedQuery = '(prefers-reduced-motion: reduce)';
const reduced = () => window.matchMedia?.(reducedQuery).matches ?? false;
function subscribeSystem(notify: () => void) {
  const media = window.matchMedia?.(reducedQuery);
  media?.addEventListener('change', notify);
  return () => media?.removeEventListener('change', notify);
}

const fallback = new ArtworkPlayback(false);
export const PlaybackContext = createContext({
  playback: fallback,
  quality: 'auto' as Quality,
  motion: 'system' as Motion,
  attempt: 0,
  setQuality: (() => {}) as (value: Quality) => void,
  setMotion: (() => {}) as (value: Motion) => void,
  retry: () => {},
});

export function useBackgroundPlayback() {
  const settings = useContext(PlaybackContext);
  const systemReduced = useSyncExternalStore(subscribeSystem, reduced);
  return {
    ...settings,
    animated: settings.motion === 'full' || (settings.motion === 'system' && !systemReduced),
  };
}
