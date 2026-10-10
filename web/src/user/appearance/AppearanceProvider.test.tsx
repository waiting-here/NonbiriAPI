import { act, render, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { expect, it, vi } from 'vitest';
import { AppearanceProvider } from './AppearanceProvider';
import type { AppearancePort, AppearanceState } from './context';

it('keeps selections account-scoped and clears cosmetics when leaving the user session', async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
  const first: AppearancePort = {
    scope: 'first',
    load: async () => ({
      selected: 'celestial',
      unlocked: ['default', 'celestial'],
      updated_at: 1,
    }),
    select: vi.fn(),
  };
  let resolve!: (state: AppearanceState) => void;
  const second: AppearancePort = {
    scope: 'second',
    load: () =>
      new Promise((r) => {
        resolve = r;
      }),
    select: vi.fn(),
  };
  const tree = (port: AppearancePort | null) => (
    <QueryClientProvider client={client}>
      <AppearanceProvider key={port?.scope ?? 'anonymous'} port={port}>
        <span>Station</span>
      </AppearanceProvider>
    </QueryClientProvider>
  );
  const root = render(tree(first));
  await waitFor(() => expect(document.documentElement.dataset.appearance).toBe('celestial'));
  root.rerender(tree(second));
  expect(document.documentElement.dataset.appearance).toBe('default');
  await act(async () => {
    resolve({ selected: 'wandering', unlocked: ['default', 'wandering'], updated_at: 2 });
  });
  await waitFor(() => expect(document.documentElement.dataset.appearance).toBe('wandering'));
  root.rerender(tree(null));
  expect(document.documentElement.dataset.appearance).toBe('default');
  root.unmount();
  expect(document.documentElement.dataset.appearance).toBeUndefined();
  client.clear();
});
