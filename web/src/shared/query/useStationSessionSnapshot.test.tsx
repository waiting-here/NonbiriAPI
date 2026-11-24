import { act, render, screen } from '@testing-library/react';
import { QueryClient, QueryClientProvider, useQuery } from '@tanstack/react-query';
import { expect, it, vi } from 'vitest';
import { useStationSessionSnapshot } from './useStationSessionSnapshot';

it('follows account changes without creating a session request or replacing the station reader', async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const key = ['user', 'session'] as const;
  const readSession = vi.fn().mockResolvedValueOnce('first').mockResolvedValueOnce('second');
  function Reader() {
    useQuery({ queryKey: key, queryFn: readSession });
    return null;
  }
  function Badge() {
    const session = useStationSessionSnapshot<string | null>('user');
    return <output>{session?.data ?? 'signed out'}</output>;
  }
  const tree = (reader: boolean) => (
    <QueryClientProvider client={client}>
      {reader && <Reader />}
      <Badge />
    </QueryClientProvider>
  );
  const view = render(tree(false));
  expect(screen.getByText('signed out')).toBeVisible();
  expect(client.getQueryState(key)).toBeUndefined();
  view.rerender(tree(true));
  await screen.findByText('first');
  await act(() => client.invalidateQueries({ queryKey: key, exact: true }));
  await screen.findByText('second');
  expect(readSession).toHaveBeenCalledTimes(2);
  act(() => client.setQueryData(key, null));
  expect(await screen.findByText('signed out')).toBeVisible();
  view.unmount();
  client.clear();
});
