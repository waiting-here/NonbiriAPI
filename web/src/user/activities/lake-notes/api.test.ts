import { afterEach, expect, test, vi } from 'vitest';
import { checkpoint } from './api';

const input = {
  generation: '1',
  expected_revision: '1',
  from_tick: 1,
  to_tick: 1,
  initial_held: false,
  edges: [],
};
function stalledFetch() {
  vi.stubGlobal(
    'fetch',
    vi.fn(
      (_path: string, options: RequestInit) =>
        new Promise<Response>((_resolve, reject) => {
          const signal = options.signal!;
          if (signal.aborted) reject(signal.reason);
          else signal.addEventListener('abort', () => reject(signal.reason), { once: true });
        }),
    ),
  );
}
afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});
test('an unresponsive checkpoint becomes an unknown outcome that can be retried', async () => {
  vi.useFakeTimers();
  stalledFetch();
  const request = checkpoint('lnc_AAAAAAAAAAAAAAAAAAAAAA', input, 'same-save-intent');
  const rejected = expect(request).rejects.toMatchObject({ code: 'network_error', status: 0 });
  await vi.advanceTimersByTimeAsync(15_000);
  await rejected;
  expect(fetch).toHaveBeenCalledOnce();
  const headers = vi.mocked(fetch).mock.calls[0][1]!.headers as Headers;
  expect(headers.get('Idempotency-Key')).toBe('same-save-intent');
});
test('leaving the page retains caller cancellation instead of reporting a network failure', async () => {
  stalledFetch();
  const owner = new AbortController();
  const request = checkpoint('lnc_AAAAAAAAAAAAAAAAAAAAAA', input, 'same-save-intent', {
    signal: owner.signal,
  });
  const rejected = expect(request).rejects.toMatchObject({ name: 'AbortError' });
  owner.abort();
  await rejected;
});
