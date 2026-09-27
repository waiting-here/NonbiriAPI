import { useCallback, useEffect, useRef, useState } from 'react';

export const FATFISH_MUSIC_KEY = 'nonbiri.games.music.v1.fatfish';
export const FATFISH_MUSIC_URL = '/assets/fatfish/music/monkeys-spinning-monkeys.mp3';

let activeMusic: HTMLAudioElement | null = null;

function readPreference(): boolean {
  try {
    return (
      typeof window !== 'undefined' && window.localStorage.getItem(FATFISH_MUSIC_KEY) === 'true'
    );
  } catch {
    return false;
  }
}

function writePreference(enabled: boolean): void {
  try {
    window.localStorage.setItem(FATFISH_MUSIC_KEY, String(enabled));
  } catch {
    // Keep the choice in memory when browser storage is unavailable.
  }
}

export function useFatFishMusic(session: object, active: boolean) {
  const [enabled, setEnabled] = useState(readPreference);
  const [unavailable, setUnavailable] = useState(false);
  const selected = useRef(enabled);
  const audio = useRef<HTMLAudioElement | null>(null);
  const host = useRef<HTMLSpanElement>(null);
  const pending = useRef(false);
  const generation = useRef(0);

  const pause = useCallback((dispose = false) => {
    generation.current++;
    pending.current = false;
    const current = audio.current;
    if (!current) return;
    current.pause();
    if (activeMusic === current) activeMusic = null;
    if (dispose) {
      current.removeAttribute('src');
      current.load();
      current.remove();
      audio.current = null;
    }
  }, []);

  const failed = useCallback(
    (request: number) => {
      if (request !== generation.current) return;
      pause();
      selected.current = false;
      setEnabled(false);
      setUnavailable(true);
      writePreference(false);
    },
    [pause],
  );

  const play = useCallback(() => {
    if (!selected.current || !active || document.visibilityState !== 'visible') return;
    let current = audio.current;
    if (!current) {
      current = new Audio(FATFISH_MUSIC_URL);
      current.loop = true;
      current.preload = 'none';
      current.setAttribute('data-fatfish-music', '');
      current.hidden = true;
      host.current?.append(current);
      audio.current = current;
    }
    if (activeMusic === current && (!current.paused || pending.current)) return;
    if (activeMusic && activeMusic !== current) activeMusic.pause();
    activeMusic = current;
    pending.current = true;
    const request = ++generation.current;
    try {
      void current
        .play()
        .then(() => {
          if (request === generation.current) pending.current = false;
        })
        .catch(() => failed(request));
    } catch {
      failed(request);
    }
  }, [active, failed]);

  const toggle = useCallback(() => {
    const next = !selected.current;
    selected.current = next;
    setEnabled(next);
    setUnavailable(false);
    writePreference(next);
    if (next) play();
    else pause();
  }, [pause, play]);

  useEffect(() => {
    if (!active) pause();
  }, [active, pause]);

  useEffect(() => () => pause(true), [session, pause]);

  useEffect(() => {
    const gesture = (event: Event) => {
      if (event.target instanceof Element && event.target.closest('[data-fatfish-music-toggle]'))
        return;
      play();
    };
    const visibility = () => {
      if (document.visibilityState === 'hidden') pause();
      else if (audio.current) play();
    };
    const hide = () => pause();
    const show = () => {
      if (audio.current) play();
    };
    window.addEventListener('pointerdown', gesture);
    window.addEventListener('keydown', gesture);
    document.addEventListener('visibilitychange', visibility);
    window.addEventListener('pagehide', hide);
    window.addEventListener('pageshow', show);
    return () => {
      window.removeEventListener('pointerdown', gesture);
      window.removeEventListener('keydown', gesture);
      document.removeEventListener('visibilitychange', visibility);
      window.removeEventListener('pagehide', hide);
      window.removeEventListener('pageshow', show);
    };
  }, [pause, play]);

  return { enabled, unavailable, toggle, host };
}
