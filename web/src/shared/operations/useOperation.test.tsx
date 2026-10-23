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
import { useOperation } from './useOperation';

function login(client: QueryClient, username: string) {
  const session = { admin: { username } };
  noteManagementSessionSuccess(
    client,
    'admin',
    session,
    beginManagementSessionRequest(client, 'admin'),
  );
  client.setQueryData(['admin', 'session'], session);
}

function setup() {
  const client = new QueryClient();
  login(client, 'example');
  let activeClient = client;
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={activeClient}>{children}</QueryClientProvider>
  );
  return {
    client,
    wrapper,
    replaceClient: (next: QueryClient) => {
      activeClient = next;
    },
  };
}

describe('transient operation secrets', () => {
  it('retains only non-secret intent and reuses the original retry key with re-entered secrets', async () => {
    const { client, wrapper } = setup();
    const keys: string[] = [];
    const received: string[] = [];
    const clearSecrets = vi.fn();
    const hook = renderHook(
      () =>
        useOperation<{ id: string }, string, string>({
          authorityRoot: ['admin', 'operations'],
          reconcile: () => undefined,
          clearSecrets,
          execute: async (_intent, secret, key) => {
            keys.push(key);
            received.push(secret ?? '');
            throw new ApiError('network_error', 'Unknown result', 0);
          },
        }),
      { wrapper },
    );
    await act(async () => {
      await hook.result.current.run({ id: '7' }, 'private-first').catch(() => undefined);
    });
    await waitFor(() => expect(hook.result.current.outcome).toBe('unknown'));
    expect(
      JSON.stringify(
        client
          .getMutationCache()
          .getAll()
          .map((mutation) => mutation.state),
      ),
    ).not.toContain('private-first');
    expect(hook.result.current.variables).toEqual({ id: '7' });
    await act(async () => {
      await hook.result.current.run({ id: '7' }, 'private-second').catch(() => undefined);
    });
    expect(keys[0]).toBe(keys[1]);
    expect(received).toEqual(['private-first', 'private-second']);
    hook.unmount();
    expect(clearSecrets).toHaveBeenCalled();
    client.clear();
  });
});

describe('operation admission during reconciliation', () => {
  it('stays busy after confirmation until refresh settles, then admits an awaited next action', async () => {
    const { client, wrapper } = setup();
    let finishRefresh!: () => void;
    const refresh = new Promise<void>((resolve) => {
      finishRefresh = resolve;
    });
    const execute = vi.fn<(intent: { id: string }, secret: string | undefined) => Promise<string>>(
      async (intent) => intent.id,
    );
    const hook = renderHook(
      () =>
        useOperation({
          authorityRoot: ['admin', 'operations'],
          execute,
          reconcile: (intent) => (intent.id === 'endpoint' ? refresh : undefined),
        }),
      { wrapper },
    );
    let first!: Promise<string>;
    act(() => {
      first = hook.result.current.run({ id: 'endpoint' });
    });
    await waitFor(() => expect(hook.result.current.outcome).toBe('confirmed'));
    expect(hook.result.current.isPending).toBe(true);
    await expect(hook.result.current.run({ id: 'key' }, 'too-early')).rejects.toMatchObject({
      code: 'operation_in_progress',
    });
    expect(execute).toHaveBeenCalledOnce();
    await act(async () => {
      finishRefresh();
      await first;
      await expect(hook.result.current.run({ id: 'key' }, 'next-secret')).resolves.toBe('key');
    });
    await waitFor(() => expect(hook.result.current.isPending).toBe(false));
    expect(execute).toHaveBeenCalledTimes(2);
    expect(execute.mock.calls[1]?.[1]).toBe('next-secret');
    hook.unmount();
    client.clear();
  });

  it.each(['cancel', 'account', 'client'] as const)(
    'allows replacement after %s without an old refresh unlocking it',
    async (boundary) => {
      const { client, wrapper, replaceClient } = setup();
      let replacementClient: QueryClient | undefined;
      let finishRefresh!: () => void;
      let finishReplacement!: () => void;
      const refresh = new Promise<void>((resolve) => {
        finishRefresh = resolve;
      });
      const replacementRefresh = new Promise<void>((resolve) => {
        finishReplacement = resolve;
      });
      const execute = vi.fn(async (intent: { id: string }) => intent.id);
      const hook = renderHook(
        () =>
          useOperation({
            authorityRoot: ['admin', 'operations'],
            execute,
            reconcile: (intent) => (intent.id === 'first' ? refresh : replacementRefresh),
          }),
        { wrapper },
      );
      let first!: Promise<unknown>;
      act(() => {
        first = hook.result.current.run({ id: 'first' }).catch((error: unknown) => error);
      });
      await waitFor(() => expect(hook.result.current.outcome).toBe('confirmed'));
      act(() => {
        if (boundary === 'cancel') hook.result.current.cancel();
        else if (boundary === 'account') login(client, 'replacement-account');
        else {
          replacementClient = new QueryClient();
          login(replacementClient, 'replacement-account');
          replaceClient(replacementClient);
          hook.rerender();
        }
      });
      expect(hook.result.current.isPending).toBe(false);
      let replacement!: Promise<string>;
      act(() => {
        replacement = hook.result.current.run({ id: 'replacement' });
      });
      await waitFor(() => expect(execute).toHaveBeenCalledTimes(2));
      await waitFor(() => expect(hook.result.current.outcome).toBe('confirmed'));
      expect(hook.result.current.isPending).toBe(true);
      await act(async () => {
        finishRefresh();
        await first;
      });
      expect(await first).toBeInstanceOf(StationSessionChangedError);
      expect(hook.result.current.isPending).toBe(true);
      await act(async () => {
        finishReplacement();
        await expect(replacement).resolves.toBe('replacement');
      });
      await waitFor(() => expect(hook.result.current.isPending).toBe(false));
      expect(hook.result.current.outcome).toBe('confirmed');
      hook.unmount();
      client.clear();
      replacementClient?.clear();
    },
  );

  it('releases admission when cancelling a refresh whose outcome is already unknown', async () => {
    const { client, wrapper } = setup();
    let finishRefresh!: () => void;
    const refresh = new Promise<void>((resolve) => {
      finishRefresh = resolve;
    });
    const hook = renderHook(
      () =>
        useOperation({
          authorityRoot: ['admin', 'operations'],
          execute: async () => {
            throw new ApiError('network_error', 'Unknown result', 0);
          },
          reconcile: () => refresh,
        }),
      { wrapper },
    );
    let request!: Promise<unknown>;
    act(() => {
      request = hook.result.current.run({ id: 'first' }).catch((error: unknown) => error);
    });
    await waitFor(() => expect(hook.result.current.outcome).toBe('unknown'));
    expect(hook.result.current.isPending).toBe(true);
    act(() => hook.result.current.cancel());
    expect(hook.result.current.outcome).toBe('unknown');
    expect(hook.result.current.isPending).toBe(false);
    await act(async () => {
      finishRefresh();
      await request;
    });
    hook.unmount();
    client.clear();
  });
});
