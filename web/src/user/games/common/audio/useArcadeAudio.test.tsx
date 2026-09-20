import { act, renderHook } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import { useArcadeAudio, type AudioFact } from './useArcadeAudio';
import { createArcadeAudio } from './engine';
import { MUSIC_QUALITY_KEY } from './preferences';

vi.mock('./engine', () => ({
  createArcadeAudio: vi.fn(() => ({
    stopEffect: vi.fn(),
    unlock: vi.fn(async () => {}),
    setMusic: vi.fn(),
    setEffectsEnabled: vi.fn(),
    play: vi.fn(),
    pause: vi.fn(),
    resume: vi.fn(async () => {}),
    close: vi.fn(),
  })),
}));
const factory = vi.mocked(createArcadeAudio);
beforeEach(() => {
  localStorage.clear();
  factory.mockClear();
});
const initial = {
  ready: true,
  scene: 'battle' as const,
  now: 1000,
  facts: [{ key: 'old', cue: 'likes_cast', at: 900 }],
};

it('does not load while disabled and consumes each confirmed fact only once', () => {
  const { result, rerender, unmount } = renderHook((props) => useArcadeAudio('likes', props), {
    initialProps: initial,
  });
  expect(factory).not.toHaveBeenCalled();
  act(() => result.current.sound.toggle());
  const engine = factory.mock.results[0].value;
  rerender({
    ...initial,
    facts: [...initial.facts, { key: 'future', cue: 'likes_cast', at: 1200 }],
  });
  expect(engine.play).not.toHaveBeenCalled();
  rerender({ ...initial, now: 1200, facts: [{ key: 'future', cue: 'likes_cast', at: 1200 }] });
  expect(engine.play).toHaveBeenCalledExactlyOnceWith('likes_cast');
  rerender({ ...initial, now: 1250, facts: [{ key: 'future', cue: 'likes_cast', at: 1200 }] });
  expect(engine.play).toHaveBeenCalledOnce();
  unmount();
  expect(engine.close).toHaveBeenCalled();
});

it('retains the old blackjack sound preference and applies quality on a fresh activation', () => {
  localStorage.setItem('nonbiri.games.sound.v1.blackjack', 'true');
  const blackjack = renderHook(() => useArcadeAudio('blackjack', { ...initial, facts: [] }));
  expect(blackjack.result.current.sound.enabled).toBe(true);
  expect(factory).toHaveBeenCalledOnce();
  expect(factory.mock.results[0].value.unlock).toHaveBeenCalledOnce();
  blackjack.unmount();
  factory.mockClear();
  const { result } = renderHook(() => useArcadeAudio('likes', { ...initial, facts: [] }));
  act(() => result.current.music.toggle());
  expect(factory.mock.calls.at(-1)?.[0].quality).toBe('light');
  localStorage.setItem(MUSIC_QUALITY_KEY, 'lossless');
  expect(factory).toHaveBeenCalledOnce();
  act(() => result.current.music.toggle());
  act(() => result.current.music.toggle());
  expect(factory.mock.calls.at(-1)?.[0].quality).toBe('lossless');
});

it('marks muted facts consumed so enabling does not replay old sounds', () => {
  const { result, rerender } = renderHook((props) => useArcadeAudio('likes', props), {
    initialProps: initial,
  });
  const facts = [{ key: 'muted', cue: 'likes_pay', at: 1200 }];
  rerender({ ...initial, now: 1200, facts });
  act(() => result.current.sound.toggle());
  rerender({ ...initial, now: 1300, facts });
  expect(factory.mock.results[0].value.play).not.toHaveBeenCalled();
});

it('consumes an entire layered result once and forwards its timed resistance accent', () => {
  const options = { ...initial, facts: [] as AudioFact[] };
  const { result, rerender } = renderHook((props) => useArcadeAudio('likes', props), {
    initialProps: options,
  });
  act(() => result.current.sound.toggle());
  const fact: AudioFact = {
    key: 'revealed-resistance',
    at: 1200,
    cue: 'likes_cleanse',
    playback: { semitones: -3 },
    accents: [
      { cue: 'common_lock', semitones: -4 },
      { cue: 'common_lock', semitones: 3, delay: 0.09 },
    ],
  };
  rerender({ ...options, now: 1200, facts: [fact] });
  const engine = factory.mock.results[0].value;
  expect(engine.play).toHaveBeenCalledTimes(3);
  expect(engine.play).toHaveBeenLastCalledWith('common_lock', fact.accents![1]);
  rerender({ ...options, now: 1300, facts: [fact] });
  expect(engine.play).toHaveBeenCalledTimes(3);
});
