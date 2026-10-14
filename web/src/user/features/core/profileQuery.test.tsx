import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, renderHook, waitFor } from '@testing-library/react';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import type { ReactNode } from 'react';
import { describe, expect, it, vi } from 'vitest';
import { coreKeys, useCoreMe } from './queries';
import { normalizeUserEnvelope } from './normalizers';

function envelope(id = '1', balance = '-0.001') {
  const value = normalizeUserEnvelope(
    JSON.parse(
      readFileSync(resolve(process.cwd(), '../internal/auth/testdata/user_envelope.json'), 'utf8'),
    ),
  );
  return { user: { ...value.user, id, balance } };
}

function harness(client: QueryClient) {
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
}

function setupFetch(id = '1', balance = '12') {
  const fetch = vi.fn(
    async () =>
      new Response(JSON.stringify(envelope(id, balance)), {
        headers: { 'Content-Type': 'application/json' },
      }),
  );
  vi.stubGlobal('fetch', fetch);
  return fetch;
}

describe('profile reuse from the current session', () => {
  it('uses a complete same-account profile with its original timestamp and negative balance', async () => {
    const client = new QueryClient();
    const originalTime = Date.now() - 2_000;
    client.setQueryData(coreKeys.session, envelope(), { updatedAt: originalTime });
    const fetch = setupFetch();
    const view = renderHook(() => useCoreMe('1'), { wrapper: harness(client) });
    expect(view.result.current.data?.user.balance).toBe('-0.001');
    expect(view.result.current.dataUpdatedAt).toBe(originalTime);
    await act(async () => {});
    expect(fetch).not.toHaveBeenCalled();
    view.unmount();
    client.clear();
  });

  it('refreshes an old profile without extending the session timestamp', async () => {
    const client = new QueryClient();
    const originalTime = Date.now() - 20_000;
    client.setQueryData(coreKeys.session, envelope(), { updatedAt: originalTime });
    const fetch = setupFetch();
    const view = renderHook(() => useCoreMe('1'), { wrapper: harness(client) });
    expect(view.result.current.dataUpdatedAt).toBe(originalTime);
    await waitFor(() => expect(view.result.current.data?.user.balance).toBe('12'));
    expect(fetch).toHaveBeenCalledTimes(1);
    expect(client.getQueryState(coreKeys.session)?.dataUpdatedAt).toBe(originalTime);
    view.unmount();
    client.clear();
  });

  it.each([undefined, null, { user: { id: '1' } }, envelope('2')])(
    'fetches the profile when the session cannot supply this account: %j',
    async (session) => {
      const client = new QueryClient();
      if (session !== undefined) client.setQueryData(coreKeys.session, session);
      const fetch = setupFetch();
      const view = renderHook(() => useCoreMe('1'), { wrapper: harness(client) });
      expect(view.result.current.data).toBeUndefined();
      await waitFor(() => expect(view.result.current.data?.user.id).toBe('1'));
      expect(fetch).toHaveBeenCalledTimes(1);
      view.unmount();
      client.clear();
    },
  );

  it('keeps wallet invalidation authoritative even after a same-account session refresh', async () => {
    const client = new QueryClient();
    client.setQueryData(coreKeys.session, envelope());
    const fetch = setupFetch();
    const view = renderHook(() => useCoreMe('1'), { wrapper: harness(client) });
    expect(fetch).not.toHaveBeenCalled();
    await act(async () => {
      client.setQueryData(coreKeys.session, envelope('1', '3'));
      await client.invalidateQueries({ queryKey: coreKeys.me('1') });
    });
    await waitFor(() => expect(view.result.current.data?.user.balance).toBe('12'));
    expect(fetch).toHaveBeenCalledTimes(1);
    view.unmount();
    client.clear();
  });

  it('does not overwrite an existing invalidated profile with newly stamped session data', async () => {
    const client = new QueryClient();
    client.setQueryData(coreKeys.me('1'), envelope('1', '8'));
    await client.invalidateQueries({ queryKey: coreKeys.me('1') });
    client.setQueryData(coreKeys.session, envelope());
    const fetch = setupFetch();
    const view = renderHook(() => useCoreMe('1'), { wrapper: harness(client) });
    expect(view.result.current.data?.user.balance).toBe('8');
    await waitFor(() => expect(view.result.current.data?.user.balance).toBe('12'));
    expect(fetch).toHaveBeenCalledTimes(1);
    view.unmount();
    client.clear();
  });

  it('uses the new account profile after a switch without carrying the old balance', async () => {
    const client = new QueryClient();
    client.setQueryData(coreKeys.session, envelope());
    const fetch = setupFetch('2', '25');
    const view = renderHook(({ id }) => useCoreMe(id), {
      initialProps: { id: '1' },
      wrapper: harness(client),
    });
    expect(view.result.current.data?.user.balance).toBe('-0.001');
    act(() => {
      client.setQueryData(coreKeys.session, envelope('2', '25'));
    });
    view.rerender({ id: '2' });
    expect(view.result.current.data?.user).toMatchObject({ id: '2', balance: '25' });
    expect(fetch).not.toHaveBeenCalled();
    view.unmount();
    client.clear();
  });
});
