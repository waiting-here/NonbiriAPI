import { afterEach, expect, it, vi } from 'vitest';
import { QueryClient, QueryObserver } from '@tanstack/react-query';
import {
  ApiError,
  apiFetch,
  isForbidden,
  isUnauthorized,
  refetchAuthoritativeQueries,
} from './http';

afterEach(() => vi.unstubAllGlobals());

it('refreshes visible pages together and leaves old pages stale until revisited', async () => {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: Infinity } },
  });
  const reads = [vi.fn(async () => 1), vi.fn(async () => 2), vi.fn(async () => 3)];
  const observers: Array<() => void> = [];
  for (let i = 0; i < 3; i++) {
    const options = { queryKey: ['rows', i], queryFn: reads[i] };
    await client.fetchQuery(options);
    if (i < 2) observers.push(new QueryObserver(client, options).subscribe(() => {}));
  }
  const release: Array<() => void> = [];
  for (const read of reads.slice(0, 2))
    read.mockImplementation(() => new Promise((resolve) => release.push(() => resolve(4))));
  const refresh = refetchAuthoritativeQueries(client, [{ queryKey: ['rows'], exact: false }]);
  await vi.waitFor(() => expect(release).toHaveLength(2));
  release.forEach((done) => done());
  expect(await refresh).toBeUndefined();
  expect(reads[2]).toHaveBeenCalledTimes(1);
  expect(client.getQueryState(['rows', 2])?.isInvalidated).toBe(true);
  await refetchAuthoritativeQueries(client, [{ queryKey: ['rows', 2] }]);
  expect(reads[2]).toHaveBeenCalledTimes(2);
  observers.forEach((stop) => stop());
  client.clear();
});

it('deduplicates overlapping targets and removes an authoritative missing detail', async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const gone = new Error('gone');
  const queryFn = vi.fn(async () => 1);
  await client.fetchQuery({ queryKey: ['rows', 'detail'], queryFn });
  queryFn.mockRejectedValue(gone);
  expect(
    await refetchAuthoritativeQueries(client, [
      { queryKey: ['rows'], exact: false },
      {
        queryKey: ['rows', 'detail'],
        ignoreError: (error) => error === gone,
        removeOnIgnoredError: true,
      },
    ]),
  ).toBeUndefined();
  expect(queryFn).toHaveBeenCalledTimes(2);
  expect(client.getQueryState(['rows', 'detail'])).toBeUndefined();
  client.clear();
});

it.each([
  'insufficient_credits',
  'insufficient_balance',
  'feature_disabled',
  'charity_suspended',
  'checkin_cap_reached',
  'mfa_invalid_code',
])('keeps business refusal %s separate from lost authority', (code) => {
  const error = new ApiError(code, 'Definite rejection', 403);
  expect(isForbidden(error)).toBe(false);
  expect(isUnauthorized(error)).toBe(false);
});

it('preserves permission, elevation, MFA and session classifications', () => {
  expect(isForbidden(new ApiError('forbidden', 'Permission lost', 403))).toBe(true);
  expect(isForbidden(new ApiError('elevated_required', 'Verify again', 403))).toBe(true);
  expect(isForbidden(new ApiError('mfa_required', 'Verify MFA', 403))).toBe(false);
  expect(isForbidden(new ApiError('steward_permission_denied', 'Permission lost', 403))).toBe(
    false,
  );
  expect(isUnauthorized(new ApiError('unauthorized', 'Sign in', 401))).toBe(true);
});

it.each([
  [403, 'insufficient_credits'],
  [403, 'forbidden'],
  [401, 'unauthorized'],
  [429, 'rate_limited'],
  [503, 'service_unavailable'],
])('preserves HTTP %s and stable code %s', async (status, code) => {
  vi.stubGlobal(
    'fetch',
    vi.fn(
      async () =>
        new Response(
          JSON.stringify({
            error: { code, message: 'Bounded server message' },
          }),
          { status, headers: { 'Content-Type': 'application/json' } },
        ),
    ),
  );
  await expect(apiFetch('/api/example')).rejects.toMatchObject({ status, code });
});

it('keeps malformed 403 responses forbidden and network failures retryable', async () => {
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => new Response('<html>proxy</html>', { status: 403 })),
  );
  await expect(apiFetch('/api/example')).rejects.toMatchObject({ code: 'forbidden', status: 403 });
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => {
      throw new TypeError('connection lost');
    }),
  );
  await expect(apiFetch('/api/example')).rejects.toMatchObject({
    code: 'network_error',
    status: 0,
  });
});

it('preserves explicit cancellation instead of reporting a network failure', async () => {
  const controller = new AbortController();
  const cancelled = new DOMException('Cancelled', 'AbortError');
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => {
      controller.abort();
      throw cancelled;
    }),
  );
  await expect(apiFetch('/api/example', { signal: controller.signal })).rejects.toBe(cancelled);
});
