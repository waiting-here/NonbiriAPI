import { useCallback, useEffect, useRef, useState } from 'react';
import { createArcadeAudio, type ArcadeAudio } from './engine';
import { isEffectCue, type AudioGame, type EffectCue, type MusicScene } from './assets';
import { readAudioPreference, readMusicQuality, writeAudioPreference } from './preferences';

export interface AudioFact {
  readonly key: string;
  readonly cue: string;
  readonly at: number;
}
export interface ArcadeSoundControl {
  readonly enabled: boolean;
  readonly toggle: () => void;
  readonly play: (cue: EffectCue) => void;
  readonly stop: (cue: EffectCue) => void;
}

export function useArcadeAudio(
  game: AudioGame,
  options: {
    readonly scene?: MusicScene;
    readonly facts: readonly AudioFact[];
    readonly now: number;
    readonly ready: boolean;
  },
) {
  const [effects, setEffects] = useState(() => readAudioPreference(game, 'sound'));
  const [music, setMusic] = useState(() => game === 'likes' && readAudioPreference(game, 'music'));
  const [unavailable, setUnavailable] = useState(false);
  const engine = useRef<ArcadeAudio | null>(null);
  const choices = useRef({ effects, music });
  const scene = useRef(options.scene ?? 'lobby');
  const seen = useRef(new Set<string>());
  const baseline = useRef(true);

  const activate = useCallback(() => {
    if (
      document.visibilityState !== 'visible' ||
      (!choices.current.music && !choices.current.effects)
    )
      return;
    if (!engine.current) {
      engine.current = createArcadeAudio({
        game,
        quality: readMusicQuality(),
        onError: () => setUnavailable(true),
      });
    }
    engine.current.setEffectsEnabled(choices.current.effects);
    engine.current.setMusic(choices.current.music ? scene.current : null);
    void engine.current.unlock();
  }, [game]);

  const toggleEffects = useCallback(() => {
    const next = !choices.current.effects;
    choices.current.effects = next;
    setEffects(next);
    setUnavailable(false);
    writeAudioPreference(game, 'sound', next);
    engine.current?.setEffectsEnabled(next);
    if (next) activate();
    else if (!choices.current.music) {
      engine.current?.close();
      engine.current = null;
    }
  }, [activate, game]);

  const toggleMusic = useCallback(() => {
    const next = !choices.current.music;
    choices.current.music = next;
    setMusic(next);
    setUnavailable(false);
    writeAudioPreference(game, 'music', next);
    if (next) {
      // Capture the browser's quality choice only on entry or a fresh activation.
      engine.current?.close();
      engine.current = null;
      activate();
    } else {
      engine.current?.setMusic(null);
      if (!choices.current.effects) {
        engine.current?.close();
        engine.current = null;
      }
    }
  }, [activate, game]);
  const play = useCallback((cue: EffectCue) => {
    if (choices.current.effects && document.visibilityState === 'visible')
      engine.current?.play(cue);
  }, []);
  const stop = useCallback((cue: EffectCue) => engine.current?.stopEffect(cue), []);

  useEffect(() => {
    scene.current = options.scene ?? 'lobby';
    engine.current?.setMusic(choices.current.music ? scene.current : null);
  }, [options.scene]);

  useEffect(() => {
    if (!options.ready) return;
    for (const fact of options.facts) {
      if (fact.at > options.now || seen.current.has(fact.key)) continue;
      seen.current.add(fact.key);
      if (!baseline.current && options.now - fact.at <= 1500 && isEffectCue(fact.cue))
        play(fact.cue);
    }
    baseline.current = false;
    while (seen.current.size > 2048) seen.current.delete(seen.current.values().next().value!);
  }, [options.facts, options.now, options.ready, play]);

  useEffect(() => {
    const visibility = () => {
      baseline.current = true;
      if (document.visibilityState !== 'visible') engine.current?.pause();
      else if (engine.current) {
        engine.current.setMusic(choices.current.music ? scene.current : null);
        void engine.current.resume();
      }
    };
    window.addEventListener('pointerdown', activate, true);
    window.addEventListener('keydown', activate, true);
    document.addEventListener('visibilitychange', visibility);
    activate();
    return () => {
      window.removeEventListener('pointerdown', activate, true);
      window.removeEventListener('keydown', activate, true);
      document.removeEventListener('visibilitychange', visibility);
      engine.current?.close();
      engine.current = null;
    };
  }, [activate]);

  return {
    sound: { enabled: effects, toggle: toggleEffects, play, stop } satisfies ArcadeSoundControl,
    music: { enabled: music, toggle: toggleMusic },
    unavailable,
  };
}
