import { useRetainedQueryData } from './useRetainedQueryData';
import { ApiError } from './http';
import { act, renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider, useQuery } from '@tanstack/react-query';
import type { ReactNode } from 'react';
import { expect, it } from 'vitest';
import { retainQueryScope } from './retain';

it('retains same-account results while cancelling replaced reads and ignores late responses', async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const pending = new Map<string, { signal: AbortSignal; resolve: (value: string) => void }>();
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
  const view = renderHook(
    ({ account, term }) =>
      useQuery({
        queryKey: ['rows', account, term],
        queryFn: ({ signal }) =>
          term === 'first'
            ? Promise.resolve('first result')
            : new Promise<string>((resolve) => pending.set(term, { signal, resolve })),
        placeholderData: retainQueryScope(['rows', account]),
      }),
    { wrapper, initialProps: { account: 'one', term: 'first' } },
  );
  await waitFor(() => expect(view.result.current.data).toBe('first result'));
  view.rerender({ account: 'one', term: 'second' });
  expect(view.result.current.data).toBe('first result');
  expect(view.result.current.isPlaceholderData).toBe(true);
  view.rerender({ account: 'one', term: 'third' });
  expect(pending.get('second')?.signal.aborted).toBe(true);
  await act(async () => pending.get('third')!.resolve('newest result'));
  await waitFor(() => expect(view.result.current.data).toBe('newest result'));
  await act(async () => pending.get('second')!.resolve('obsolete result'));
  expect(view.result.current.data).toBe('newest result');
  view.rerender({ account: 'two', term: 'other-account' });
  expect(view.result.current.data).toBeUndefined();
  expect(view.result.current.isPending).toBe(true);
  view.unmount();
  expect(pending.get('other-account')?.signal.aborted).toBe(true);
  client.clear();
});

it('preserves the previous read after a transient replacement failure and clears it on revoked access', async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
  const view = renderHook(
    ({ term }) => {
      const query = useQuery({
        queryKey: ['rows', 'one', term],
        queryFn: async () => {
          if (term === 'offline') throw new ApiError('service_unavailable', 'Timed out', 503);
          if (term === 'revoked') throw new ApiError('forbidden', 'Permission changed', 403);
          return 'saved result';
        },
      });
      return { query, data: useRetainedQueryData(query, ['rows', 'one']) };
    },
    { wrapper, initialProps: { term: 'first' } },
  );
  await waitFor(() => expect(view.result.current.data).toBe('saved result'));
  view.rerender({ term: 'offline' });
  await waitFor(() => expect(view.result.current.query.isError).toBe(true));
  expect(view.result.current.query.data).toBeUndefined();
  expect(view.result.current.data).toBe('saved result');
  view.rerender({ term: 'revoked' });
  await waitFor(() => expect(view.result.current.query.isError).toBe(true));
  expect(view.result.current.data).toBeUndefined();
  view.unmount();
  client.clear();
});
