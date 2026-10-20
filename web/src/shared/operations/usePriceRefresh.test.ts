import { act, renderHook } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { usePriceRefresh } from './usePriceRefresh';

afterEach(() => vi.useRealTimers());

it('refreshes once at the earliest server boundary despite a skewed device clock and cached age', async () => {
  vi.useFakeTimers();
  vi.setSystemTime(new Date('2040-01-01'));
  const received = Date.now() - 2000,
    refresh = vi.fn().mockResolvedValue(undefined);
  renderHook(() => usePriceRefresh(100, [null, 110, 105], received, refresh));
  await act(() => vi.advanceTimersByTimeAsync(2999));
  expect(refresh).not.toHaveBeenCalled();
  await act(() => vi.advanceTimersByTimeAsync(1));
  expect(refresh).toHaveBeenCalledTimes(1);
  await act(() => vi.advanceTimersByTimeAsync(60_000));
  expect(refresh).toHaveBeenCalledTimes(1);
});

it('reschedules new snapshots and stops when hidden or unmounted', async () => {
  vi.useFakeTimers();
  const refresh = vi.fn().mockResolvedValue(undefined),
    received = Date.now();
  const view = renderHook(
    ({ next, enabled }) => usePriceRefresh(100, [next], received, refresh, enabled),
    { initialProps: { next: 105, enabled: true } },
  );
  view.rerender({ next: 110, enabled: true });
  await act(() => vi.advanceTimersByTimeAsync(5000));
  expect(refresh).not.toHaveBeenCalled();
  view.rerender({ next: 110, enabled: false });
  await act(() => vi.advanceTimersByTimeAsync(6000));
  expect(refresh).not.toHaveBeenCalled();
  view.unmount();
});

it('leaves query failure recovery to the caller without a retry loop', async () => {
  vi.useFakeTimers();
  const refresh = vi.fn().mockRejectedValue(new Error('offline'));
  renderHook(() => usePriceRefresh(100, [101], Date.now(), refresh));
  await act(() => vi.advanceTimersByTimeAsync(60_000));
  expect(refresh).toHaveBeenCalledTimes(1);
});
