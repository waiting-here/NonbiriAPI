import { useEffect, useRef } from 'react';
import { useGameVisibility } from '../common/visibility';
import type { EffectCue } from '../common/audio/assets';

export function useActionWarning(
  identity: string,
  remaining: number | null,
  eligible: boolean,
  play: (cue: EffectCue) => void,
  stop: (cue: EffectCue) => void,
) {
  const visible = useGameVisibility();
  const previous = useRef({
    identity: '',
    remaining: null as number | null,
    visible: false,
    seen: new Set<number>(),
  });
  const urgent = eligible && remaining !== null && remaining > 0 && remaining < 5;
  useEffect(() => () => stop('likes_countdown'), [identity, urgent, visible, stop]);
  useEffect(() => {
    const last = previous.current;
    if (last.identity !== identity) {
      last.identity = identity;
      last.seen.clear();
      last.remaining = null;
    }
    const resumed = !last.visible && visible;
    const changed = last.remaining !== remaining;
    last.visible = visible;
    last.remaining = remaining;
    if (!urgent || remaining === null || last.seen.has(remaining)) return;
    last.seen.add(remaining);
    if (visible && !resumed && changed) play('likes_countdown');
  }, [identity, remaining, urgent, visible, play]);
  return urgent;
}
