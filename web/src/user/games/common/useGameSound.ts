import { useCallback, useEffect, useRef, useState } from 'react';
import { createGameSound, type GameSound, type GameSoundCue } from './sound';

type SoundGame = 'linklink' | 'fishing' | 'rps';

export interface GameSoundControl {
  readonly enabled: boolean;
  readonly toggle: () => void;
  readonly play: (cue: GameSoundCue) => void;
}

function preferenceKey(game: SoundGame): string {
  return `nonbiri.games.sound.v1.${game}`;
}

function readPreference(game: SoundGame): boolean {
  try {
    return localStorage.getItem(preferenceKey(game)) === 'true';
  } catch {
    return false;
  }
}

export function useGameSound(game: SoundGame): GameSoundControl {
  const [enabled, setEnabled] = useState(() => readPreference(game));
  const enabledRef = useRef(enabled);
  const engine = useRef<GameSound | null>(null);
  const activate = useCallback(() => {
    if (!enabledRef.current || document.visibilityState !== 'visible') return;
    engine.current ??= createGameSound();
    void engine.current.unlock();
  }, []);
  const play = useCallback((cue: GameSoundCue) => {
    if (enabledRef.current && document.visibilityState === 'visible') engine.current?.play(cue);
  }, []);
  const toggle = useCallback(() => {
    const next = !enabledRef.current;
    enabledRef.current = next;
    setEnabled(next);
    try {
      localStorage.setItem(preferenceKey(game), String(next));
    } catch {
      // Keep the choice for this page even when browser storage is unavailable.
    }
    if (next) activate();
    else engine.current?.silence();
  }, [activate, game]);

  useEffect(() => {
    const visibilityChanged = () => {
      if (document.visibilityState !== 'visible') engine.current?.silence();
    };
    // A remembered preference grants no autoplay: only a new input gesture
    // creates or resumes the context. Events received before that stay silent.
    window.addEventListener('pointerdown', activate, true);
    window.addEventListener('keydown', activate, true);
    document.addEventListener('visibilitychange', visibilityChanged);
    return () => {
      window.removeEventListener('pointerdown', activate, true);
      window.removeEventListener('keydown', activate, true);
      document.removeEventListener('visibilitychange', visibilityChanged);
      engine.current?.close();
      engine.current = null;
    };
  }, [activate]);

  return { enabled, toggle, play };
}
