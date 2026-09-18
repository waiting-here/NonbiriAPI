import { useState } from 'react';
import type { AudioFact } from './useArcadeAudio';

/** Keep only the adjacent confirmed snapshots; history views never enter this path. */
export function useSnapshotAudioFacts<T>(
  snapshot: T | undefined,
  extract: (current: T, previous: T | undefined) => readonly AudioFact[],
): readonly AudioFact[] {
  const [pair, setPair] = useState<{ current: T | undefined; previous: T | undefined }>({
    current: snapshot,
    previous: undefined,
  });
  if (pair.current !== snapshot) setPair({ current: snapshot, previous: pair.current });
  return snapshot ? extract(snapshot, pair.previous) : [];
}
