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
import { useRetainedOperation, type OperationContext } from './useRetainedOperation';

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
  it.each(['success', 'error'] as const)(
    'clears %s feedback when the next edit resets it',
    async (result) => {
      const { client, wrapper } = setup();
      const execute = vi.fn(async () => {
        if (result === 'error') throw new ApiError('invalid_input', 'Fix this input.', 400);
        return 'saved';
      });
      const hook = renderHook(() => useRetainedOperation(execute, vi.fn()), { wrapper });
      await act(async () => {
        await hook.result.current.mutateAsync({ note: 'first edit' }).catch(() => undefined);
      });
      expect(hook.result.current.isSuccess).toBe(result === 'success');
      expect(hook.result.current.isError).toBe(result === 'error');
      act(() => hook.result.current.reset());
      expect(hook.result.current.outcome).toBe('idle');
      expect(hook.result.current.isSuccess).toBe(false);
      expect(hook.result.current.isError).toBe(false);
      expect(hook.result.current.error).toBeNull();
      expect(hook.result.current.data).toBeUndefined();
      hook.unmount();
      client.clear();
    },
  );

  it('keeps an unknown retry identity after clearing feedback for an edit', async () => {
    const { client, wrapper } = setup();
    const execute = vi.fn<(input: { note: string }, key: string) => Promise<never>>(async () => {
      throw new ApiError('network_error', 'Unknown', 0);
    });
    const hook = renderHook(() => useRetainedOperation(execute, vi.fn()), { wrapper });
    await act(async () => {
      await hook.result.current.mutateAsync({ note: 'original' }).catch(() => undefined);
    });
    expect(hook.result.current.outcome).toBe('unknown');
    act(() => hook.result.current.reset());
    expect(hook.result.current.isError).toBe(false);
    for (const note of ['edited', 'original']) {
      await act(async () => {
        await hook.result.current.mutateAsync({ note }).catch(() => undefined);
      });
    }
    expect(execute.mock.calls[0]?.[1]).toBe(execute.mock.calls[2]?.[1]);
    expect(execute.mock.calls[0]?.[1]).not.toBe(execute.mock.calls[1]?.[1]);
    hook.unmount();
    client.clear();
  });

  it('cancels a queued action without treating it as a dispatched transaction', async () => {
    const { client, wrapper } = setup();
    const execute = vi.fn(async () => 'saved');
    const hook = renderHook(() => useRetainedOperation(execute, vi.fn()), { wrapper });
    let request!: Promise<unknown>;
    act(() => {
      request = hook.result.current.mutateAsync({ id: '7' }).catch((error: unknown) => error);
      hook.result.current.cancel();
    });
    await act(async () => {
      await request;
    });
    expect(await request).toBeInstanceOf(StationSessionChangedError);
    expect(execute).not.toHaveBeenCalled();
    expect(hook.result.current.outcome).toBe('idle');
    hook.unmount();
    client.clear();
  });
  it('checks an unknown outcome with its original identity and accepts explicit authoritative confirmation', async () => {
    const { client, wrapper } = setup();
    let sentKey = '';
    const execute = vi.fn(async (_input: { id: string }, key: string) => {
      sentKey = key;
      throw new ApiError('network_error', 'Unknown', 0);
    });
    let confirmed = false;
    const reconcile = vi.fn(
      async (_input: { id: string }, _error: unknown, context: OperationContext) => {
        expect(context.operationKey).toBe(sentKey);
        return confirmed ? { operationConfirmed: true } : undefined;
      },
    );
    const hook = renderHook(() => useRetainedOperation(execute, reconcile), { wrapper });
    await act(async () => {
      await hook.result.current.mutateAsync({ id: '7' }).catch(() => undefined);
    });
    expect(hook.result.current.outcome).toBe('unknown');
    confirmed = true;
    await act(async () => {
      await hook.result.current.check();
    });
    expect(hook.result.current.outcome).toBe('confirmed');
    expect(hook.result.current.error).toBeNull();
    expect(execute).toHaveBeenCalledOnce();
    hook.unmount();
    client.clear();
  });
  it('captures an immutable click snapshot and coalesces duplicate clicks', async () => {
    const { client, wrapper } = setup();
    let finish!: (value: string) => void;
    const execute = vi.fn<
      (input: { note: string; config: { enabled: boolean } }) => Promise<string>
    >(async () => {
      return new Promise<string>((resolve) => {
        finish = resolve;
      });
    });
    const hook = renderHook(() => useRetainedOperation(execute, vi.fn()), { wrapper });
    const input = { note: 'original', config: { enabled: true } };
    let first!: Promise<string>;
    act(() => {
      first = hook.result.current.mutateAsync(input);
      expect(hook.result.current.mutateAsync(input)).toBe(first);
      input.note = 'changed';
      input.config.enabled = false;
    });
    await waitFor(() => expect(execute).toHaveBeenCalledOnce());
    expect(execute.mock.calls[0]?.[0]).toEqual({ note: 'original', config: { enabled: true } });
    await act(async () => {
      finish('saved');
      await first;
    });
    hook.unmount();
    client.clear();
  });

  it('keeps a confirmed save when its refresh fails and retries only the read', async () => {
    const { client, wrapper } = setup();
    const execute = vi.fn(async () => 'saved');
    const reconcile = vi
      .fn()
      .mockRejectedValueOnce(new Error('Read failed'))
      .mockResolvedValue(undefined);
    const hook = renderHook(() => useRetainedOperation(execute, reconcile), { wrapper });
    await act(async () => {
      await expect(hook.result.current.mutateAsync({ id: '7' })).resolves.toBe('saved');
    });
    expect(hook.result.current.outcome).toBe('refresh-failed');
    await act(async () => {
      await hook.result.current.refresh();
    });
    expect(hook.result.current.outcome).toBe('confirmed');
    expect(execute).toHaveBeenCalledOnce();
    expect(reconcile).toHaveBeenCalledTimes(2);
    hook.unmount();
    client.clear();
  });

  it.each(['cancel', 'unmount', 'account'] as const)(
    'blocks late downloads, cache writes and follow-up requests after %s',
    async (boundary) => {
      const { client, wrapper } = setup();
      let finish!: () => void;
      let context!: OperationContext;
      const effects = vi.fn();
      const clearSecrets = vi.fn();
      const execute = vi.fn(
        async (_input: { id: string }, _key: string, request: OperationContext) => {
          context = request;
          await new Promise<void>((resolve) => {
            finish = resolve;
          });
          request.commit(effects);
          return 'saved';
        },
      );
      const hook = renderHook(
        () => useRetainedOperation(execute, vi.fn(), ['admin', 'operations'], { clearSecrets }),
        { wrapper },
      );
      let request!: Promise<unknown>;
      act(() => {
        request = hook.result.current.mutateAsync({ id: '7' }).catch((error: unknown) => error);
      });
      await waitFor(() => expect(execute).toHaveBeenCalledOnce());
      act(() => {
        if (boundary === 'cancel') hook.result.current.cancel();
        else if (boundary === 'account') login(client, 'second');
        else hook.unmount();
      });
      expect(context.signal.aborted).toBe(true);
      await act(async () => {
        finish();
        await request;
      });
      expect(await request).toBeInstanceOf(StationSessionChangedError);
      expect(effects).not.toHaveBeenCalled();
      expect(clearSecrets).toHaveBeenCalled();
      hook.unmount();
      client.clear();
    },
  );

  it('retains separate identities when editing between two unknown outcomes', async () => {
    const { client, wrapper } = setup();
    const keys: Array<[string, string]> = [];
    const execute = async (input: { note: string }, key: string) => {
      keys.push([input.note, key]);
      throw new ApiError('network_error', 'Unknown', 0);
    };
    const hook = renderHook(() => useRetainedOperation(execute, vi.fn()), { wrapper });
    for (const note of ['first', 'second', 'first']) {
      await act(async () => {
        await hook.result.current.mutateAsync({ note }).catch(() => undefined);
      });
    }
    expect(keys[0]?.[1]).toBe(keys[2]?.[1]);
    expect(keys[0]?.[1]).not.toBe(keys[1]?.[1]);
    hook.unmount();
    client.clear();
  });

  it('rejects a late resolved promise if the account changes during reconciliation', async () => {
    const { client, wrapper } = setup();
    let finish!: () => void;
    const reconcile = vi.fn(
      () =>
        new Promise<void>((resolve) => {
          finish = resolve;
        }),
    );
    const hook = renderHook(() => useRetainedOperation(async () => 'saved', reconcile), {
      wrapper,
    });
    let request!: Promise<unknown>;
    act(() => {
      request = hook.result.current.mutateAsync({ id: '7' }).catch((error: unknown) => error);
    });
    await waitFor(() => expect(reconcile).toHaveBeenCalledOnce());
    act(() => login(client, 'second'));
    await act(async () => {
      finish();
      await request;
    });
    expect(await request).toBeInstanceOf(StationSessionChangedError);
    expect(hook.result.current.data).toBeUndefined();
    hook.unmount();
    client.clear();
  });
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
