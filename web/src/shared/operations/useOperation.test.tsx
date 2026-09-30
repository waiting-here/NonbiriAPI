import { act, renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ReactNode } from 'react';
import { describe, expect, it, vi } from 'vitest';
import {
  beginManagementSessionRequest,
  noteManagementSessionSuccess,
} from '@shared/charityManagement';
import { ApiError } from '@shared/query/http';
import { useOperation } from './useOperation';

describe('transient operation secrets', () => {
  it('retains only non-secret intent and reuses the original retry key with re-entered secrets', async () => {
    const client = new QueryClient();
    const session = { admin: { username: 'example' } };
    noteManagementSessionSuccess(
      client,
      'admin',
      session,
      beginManagementSessionRequest(client, 'admin'),
    );
    client.setQueryData(['admin', 'session'], session);
    const wrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    );
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
