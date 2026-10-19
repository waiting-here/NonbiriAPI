import { act, renderHook } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { useRateLimitRefresh } from './useRateLimitRefresh';

afterEach(() => vi.useRealTimers());

describe('rate-limit deadline refresh', () => {
  it('reads the server at the earliest deadline and schedules the next remaining cooldown', async () => {
    vi.useFakeTimers();
    vi.setSystemTime(1_000_000);
    const refresh = vi.fn().mockResolvedValue(undefined);
    const { rerender, unmount } = renderHook(
      ({ deadlines }) => useRateLimitRefresh(deadlines, refresh),
      { initialProps: { deadlines: [1002, 1005] } },
    );
    await act(() => vi.advanceTimersByTimeAsync(1999));
    expect(refresh).not.toHaveBeenCalled();
    await act(() => vi.advanceTimersByTimeAsync(1));
    expect(refresh).toHaveBeenCalledTimes(1);
    rerender({ deadlines: [1005] });
    await act(() => vi.advanceTimersByTimeAsync(3000));
    expect(refresh).toHaveBeenCalledTimes(2);
    unmount();
    await act(() => vi.advanceTimersByTimeAsync(1000));
    expect(refresh).toHaveBeenCalledTimes(2);
  });

  it('does not overflow a long timeout or loop a failed expired-state read', async () => {
    vi.useFakeTimers();
    vi.setSystemTime(1_000_000);
    const refresh = vi.fn().mockRejectedValue(new Error('Unavailable'));
    const { rerender } = renderHook(({ deadlines }) => useRateLimitRefresh(deadlines, refresh), {
      initialProps: { deadlines: [3_000_000] },
    });
    await act(() => vi.advanceTimersByTimeAsync(2_147_483_647));
    expect(refresh).not.toHaveBeenCalled();
    rerender({ deadlines: [1000] });
    await act(() => vi.advanceTimersByTimeAsync(0));
    expect(refresh).toHaveBeenCalledTimes(1);
    rerender({ deadlines: [1000] });
    await act(() => vi.advanceTimersByTimeAsync(10000));
    expect(refresh).toHaveBeenCalledTimes(1);
  });
});
