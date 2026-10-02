import { afterEach, expect, it, vi } from 'vitest';
import { fetchRecurrence } from './time';

afterEach(() => vi.unstubAllGlobals());

it('preserves the read-only server preview without deriving calendar boundaries in the browser', async () => {
  const transition = {
    instant: 123,
    local: '2026-10-27T03:00:01',
    time_zone: 'UTC',
    offset_seconds: 0,
    adjustment: 'none',
  };
  vi.stubGlobal(
    'fetch',
    vi.fn(
      async () =>
        new Response(JSON.stringify({ transitions: [transition] }), {
          headers: { 'content-type': 'application/json' },
        }),
    ),
  );
  expect(await fetchRecurrence('admin', '2026-10-27T03:00:01', 'month', 'UTC', 456)).toEqual([
    transition,
  ]);
});
