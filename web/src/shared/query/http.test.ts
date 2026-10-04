import { expect, it, vi } from 'vitest';
import { QueryClient, QueryObserver } from '@tanstack/react-query';
import { refetchAuthoritativeQueries } from './http';

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
