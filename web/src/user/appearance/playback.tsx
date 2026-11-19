import { useMemo, useState, type ReactNode } from 'react';
import { ArtworkPlayback } from '@shared/animation/artwork-playback';
import { PlaybackContext, type Quality, type Motion } from './playback-context';

function stored<T extends string>(key: string, choices: readonly T[]): T {
  try {
    const value = localStorage.getItem(key) as T;
    if (choices.includes(value)) return value;
  } catch {
    /* Device storage can be unavailable. */
  }
  return choices[0];
}

function save(key: string, value: string) {
  try {
    localStorage.setItem(key, value);
  } catch {
    /* Keep the choice for this session. */
  }
}

export function BackgroundPlayback({
  sceneKey,
  children,
}: {
  sceneKey: string;
  children: ReactNode;
}) {
  const [quality, chooseQuality] = useState<Quality>(() =>
    stored('nb.appearance-quality', ['auto', 'highest']),
  );
  const [motion, chooseMotion] = useState<Motion>(() =>
    stored('nb.appearance-motion', ['system', 'full', 'static']),
  );
  const [attempt, setAttempt] = useState(0);
  const key = [sceneKey, motion, quality, attempt].join('/');
  const playback = useMemo(() => new ArtworkPlayback(quality === 'highest', key), [quality, key]);
  return (
    <PlaybackContext
      value={{
        playback,
        quality,
        motion,
        attempt,
        setMotion(value) {
          save('nb.appearance-motion', value);
          chooseMotion(value);
        },
        setQuality(value) {
          save('nb.appearance-quality', value);
          chooseQuality(value);
        },
        retry() {
          setAttempt((n) => n + 1);
        },
      }}
    >
      {children}
    </PlaybackContext>
  );
}
