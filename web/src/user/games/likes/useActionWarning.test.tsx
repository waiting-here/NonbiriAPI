import { act, renderHook } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { useActionWarning } from './useActionWarning';

afterEach(() => vi.restoreAllMocks());
describe('action deadline warning', () => {
  it('warns only below five seconds, once per second and phase while eligible', () => {
    const play = vi.fn();
    const stop = vi.fn();
    const { result, rerender } = renderHook(
      ({ seconds, eligible = true, phase = 'one' }) =>
        useActionWarning(phase, seconds, eligible, play, stop),
      { initialProps: { seconds: 5, eligible: true, phase: 'one' } },
    );
    expect(result.current).toBe(false);
    for (const seconds of [4, 4, 3, 3, 2, 1, 1, 0])
      rerender({ seconds, eligible: true, phase: 'one' });
    expect(play.mock.calls).toEqual(Array.from({ length: 4 }, () => ['likes_countdown']));
    expect(result.current).toBe(false);
    rerender({ seconds: 3, eligible: false, phase: 'two' });
    expect(result.current).toBe(false);
    expect(play).toHaveBeenCalledTimes(4);
    expect(stop).toHaveBeenCalledWith('likes_countdown');
  });
  it('consumes hidden ticks and resumes without replaying or duplicating the current tick', () => {
    const visibility = vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('visible');
    const play = vi.fn();
    const stop = vi.fn();
    const { rerender, unmount } = renderHook(
      ({ seconds }) => useActionWarning('one', seconds, true, play, stop),
      { initialProps: { seconds: 5 } },
    );
    visibility.mockReturnValue('hidden');
    act(() => document.dispatchEvent(new Event('visibilitychange')));
    rerender({ seconds: 4 });
    rerender({ seconds: 3 });
    visibility.mockReturnValue('visible');
    act(() => document.dispatchEvent(new Event('visibilitychange')));
    expect(play).not.toHaveBeenCalled();
    rerender({ seconds: 2 });
    rerender({ seconds: 1 });
    expect(play).toHaveBeenCalledTimes(2);
    unmount();
  });
});
