import { act, renderHook } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { useAuthoritativeCountdown } from './countdown';

describe('authoritative countdown projection', () => {
  afterEach(() => vi.useRealTimers());

  it('counts from the server projection, requests one re-read at zero, and resets by identity', () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-08-31T12:00:00Z'));
    const elapsed = vi.fn();
    const { result, rerender } = renderHook(
      ({ identity, deadline, serverNow }) =>
        useAuthoritativeCountdown(identity, deadline, serverNow, elapsed),
      { initialProps: { identity: 'session:1', deadline: 102, serverNow: 100 } },
    );

    expect(result.current).toBe(2);
    act(() => vi.advanceTimersByTime(1_000));
    expect(result.current).toBe(1);
    act(() => vi.advanceTimersByTime(1_000));
    expect(result.current).toBe(0);
    expect(elapsed).toHaveBeenCalledTimes(1);
    act(() => vi.advanceTimersByTime(2_000));
    expect(elapsed).toHaveBeenCalledTimes(1);

    rerender({ identity: 'session:1', deadline: 102, serverNow: 102 });
    act(() => vi.advanceTimersByTime(250));
    expect(elapsed).toHaveBeenCalledTimes(1);

    rerender({ identity: 'session:2', deadline: 205, serverNow: 200 });
    expect(result.current).toBe(5);
  });

  it('does not invent a deadline for a terminal projection', () => {
    vi.useFakeTimers();
    const elapsed = vi.fn();
    const { result } = renderHook(() => useAuthoritativeCountdown('terminal', null, 100, elapsed));
    act(() => vi.advanceTimersByTime(10_000));
    expect(result.current).toBeNull();
    expect(elapsed).not.toHaveBeenCalled();
  });
});
