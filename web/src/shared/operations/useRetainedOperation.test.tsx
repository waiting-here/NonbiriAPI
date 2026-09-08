import { act, renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ReactNode } from 'react';
import { describe, expect, it, vi } from 'vitest';
import {
  beginManagementSessionRequest,
  noteManagementSessionSuccess,
  StationSessionChangedError,
} from '@shared/charityManagement';
import { ApiError } from '@shared/query/http';
import { useRetainedOperation } from './useRetainedOperation';

function login(client: QueryClient, username: string) {
  const session = { admin: { username } };
  const generation = beginManagementSessionRequest(client, 'admin');
  noteManagementSessionSuccess(client, 'admin', session, generation);
  client.setQueryData(['admin', 'session'], session);
}

function setup(loggedIn = true) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  if (loggedIn) login(client, 'first');
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
  return { client, wrapper };
}

describe('retained operation station authority', () => {
  it('does not dispatch queued variables after the station account changes', async () => {
    const { client, wrapper } = setup();
    const execute = vi.fn(async () => 'value');
    const reconcile = vi.fn();
    const hook = renderHook(() => useRetainedOperation(execute, reconcile), { wrapper });
    let pending!: Promise<unknown>;
    act(() => {
      pending = hook.result.current.mutateAsync({ id: '7' }).catch((error: unknown) => error);
      login(client, 'second');
    });
    await act(async () => {
      await pending;
    });
    expect(await pending).toBeInstanceOf(StationSessionChangedError);
    expect(execute).not.toHaveBeenCalled();
    expect(reconcile).not.toHaveBeenCalled();
    hook.unmount();
    client.clear();
  });
  it.each([200, 401, 403, 409, 503])(
    'ignores a late %s outcome after another account signs in',
    async (status) => {
      const { client, wrapper } = setup();
      let resolve!: (value: string) => void;
      let reject!: (error: Error) => void;
      const request = new Promise<string>((yes, no) => {
        resolve = yes;
        reject = no;
      });
      const execute = vi.fn(() => request);
      const reconcile = vi.fn();
      const hook = renderHook(() => useRetainedOperation(execute, reconcile), { wrapper });
      let pending!: Promise<unknown>;
      act(() => {
        pending = hook.result.current.mutateAsync({ id: '7' }).catch((error: unknown) => error);
      });
      await waitFor(() => expect(execute).toHaveBeenCalledOnce());
      act(() => {
        login(client, 'second');
        client.setQueryData(['admin', 'operations', 'private'], 'second account');
      });
      await act(async () => {
        if (status === 200) resolve('first account result');
        else reject(new ApiError('failure', 'Delayed failure.', status));
        await pending;
      });
      expect(await pending).toBeInstanceOf(StationSessionChangedError);
      expect(client.getQueryData(['admin', 'session'])).toEqual({ admin: { username: 'second' } });
      expect(client.getQueryData(['admin', 'operations', 'private'])).toBe('second account');
      expect(reconcile).not.toHaveBeenCalled();
      hook.unmount();
      client.clear();
    },
  );

  it('rejects a write without a confirmed session', async () => {
    const { client, wrapper } = setup(false);
    const execute = vi.fn(async () => 'value');
    const hook = renderHook(() => useRetainedOperation(execute, vi.fn()), { wrapper });
    await act(async () => {
      await expect(hook.result.current.mutateAsync({ id: '7' })).rejects.toBeInstanceOf(
        StationSessionChangedError,
      );
    });
    expect(execute).not.toHaveBeenCalled();
    hook.unmount();
    client.clear();
  });

  it('retains the retry key for one subject and renews it for another subject', async () => {
    const { client, wrapper } = setup();
    const keys: string[] = [];
    const execute = vi.fn(async (_input: { id: string }, key: string) => {
      keys.push(key);
      throw new ApiError('unavailable', 'Unknown outcome.', 503);
    });
    const reconcile = vi.fn();
    const hook = renderHook(() => useRetainedOperation(execute, reconcile), { wrapper });
    for (let index = 0; index < 2; index++) {
      await act(async () => {
        await expect(hook.result.current.mutateAsync({ id: '7' })).rejects.toBeInstanceOf(ApiError);
      });
    }
    expect(keys[0]).toBe(keys[1]);
    act(() => login(client, 'second'));
    await act(async () => {
      await expect(hook.result.current.mutateAsync({ id: '7' })).rejects.toBeInstanceOf(ApiError);
    });
    expect(keys[2]).not.toBe(keys[0]);
    expect(reconcile).toHaveBeenCalledTimes(3);
    hook.unmount();
    client.clear();
  });

  it('reuses the receipt key when the same account confirms a new session generation', async () => {
    const { client, wrapper } = setup();
    let resolve!: (value: string) => void;
    const pendingRequest = new Promise<string>((done) => {
      resolve = done;
    });
    const keys: string[] = [];
    const execute = vi.fn(async (_input: { id: string }, key: string) => {
      keys.push(key);
      return keys.length === 1 ? pendingRequest : 'committed receipt';
    });
    const reconcile = vi.fn();
    const hook = renderHook(() => useRetainedOperation(execute, reconcile), { wrapper });
    let pending!: Promise<unknown>;
    act(() => {
      pending = hook.result.current.mutateAsync({ id: '7' }).catch((error: unknown) => error);
    });
    await waitFor(() => expect(execute).toHaveBeenCalledOnce());
    act(() => login(client, 'first'));
    await act(async () => {
      resolve('committed receipt');
      await pending;
    });
    expect(await pending).toBeInstanceOf(StationSessionChangedError);
    await act(async () => {
      await expect(hook.result.current.mutateAsync({ id: '7' })).resolves.toBe('committed receipt');
    });
    expect(keys[0]).toBe(keys[1]);
    expect(reconcile).toHaveBeenCalledOnce();
    hook.unmount();
    client.clear();
  });

  it('closes the current account on its own forbidden response', async () => {
    const { client, wrapper } = setup();
    client.setQueryData(['admin', 'operations', 'private'], 'first account');
    const reconcile = vi.fn();
    const execute = vi.fn(async () => {
      throw new ApiError('forbidden', 'Forbidden.', 403);
    });
    const hook = renderHook(() => useRetainedOperation(execute, reconcile), { wrapper });
    await act(async () => {
      await expect(hook.result.current.mutateAsync({ id: '7' })).rejects.toBeInstanceOf(ApiError);
    });
    expect(client.getQueryData(['admin', 'session'])).toBeNull();
    expect(client.getQueryData(['admin', 'operations', 'private'])).toBeUndefined();
    expect(reconcile).not.toHaveBeenCalled();
    hook.unmount();
    client.clear();
  });
});
