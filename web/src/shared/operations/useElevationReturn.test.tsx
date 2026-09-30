import { renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { StrictMode, type ReactNode } from 'react';
import { describe, expect, it, vi } from 'vitest';
import {
  beginManagementSessionRequest,
  noteManagementSessionSuccess,
} from '@shared/charityManagement';
import { writePendingElevation, type ElevationIntent } from './elevation';
import { useElevationReturn } from './useElevationReturn';
import type { OperationContext } from './useRetainedOperation';

function fixture() {
  const client = new QueryClient();
  const session = { user: { id: '7', username: 'Example', level: 1, effective_level: 1 } };
  noteManagementSessionSuccess(
    client,
    'steward',
    session,
    beginManagementSessionRequest(client, 'steward'),
  );
  client.setQueryData(['user', 'session'], session);
  const wrapper = ({ children }: { children: ReactNode }) => (
    <StrictMode>
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    </StrictMode>
  );
  return { client, wrapper };
}
describe('elevation return', () => {
  it('drops the pending capability when the account changes before resuming', async () => {
    const { client, wrapper } = fixture();
    writePendingElevation('export', '7');
    document.cookie = 'nb_elevated=example-capability; Path=/';
    const resume = vi.fn();
    const hook = renderHook(() => useElevationReturn('7', resume), { wrapper });
    client.setQueryData(['user', 'session'], null);
    await Promise.resolve();
    expect(resume).not.toHaveBeenCalled();
    expect(sessionStorage.length).toBe(0);
    expect(document.cookie).not.toContain('nb_elevated');
    hook.unmount();
    client.clear();
  });
  it('resumes when unrelated cached data changes before consuming the return', async () => {
    const { client, wrapper } = fixture();
    writePendingElevation('export', '7');
    document.cookie = 'nb_elevated=example-capability; Path=/';
    const resume = vi.fn();
    const hook = renderHook(() => useElevationReturn('7', resume), { wrapper });
    client.setQueryData(['user', 'activity'], { title: 'Updated' });
    await waitFor(() => expect(resume).toHaveBeenCalledOnce());
    hook.unmount();
    client.clear();
  });
  it('continues the original export once under mount rehearsal, consuming the capability', async () => {
    const { client, wrapper } = fixture();
    writePendingElevation('export', '7');
    document.cookie = 'nb_elevated=example-capability; Path=/';
    const resume = vi.fn();
    const hook = renderHook(() => useElevationReturn('7', resume), { wrapper });
    await waitFor(() => expect(resume).toHaveBeenCalledOnce());
    expect(resume.mock.calls[0]?.slice(0, 2)).toEqual(['export', 'example-capability']);
    expect(document.cookie).not.toContain('nb_elevated');
    expect(sessionStorage.length).toBe(0);
    hook.unmount();
    client.clear();
  });
  it.each(['missing', 'other-account'] as const)(
    'does not download after a %s return',
    async (kind) => {
      const { client, wrapper } = fixture();
      writePendingElevation('export', kind === 'other-account' ? '8' : '7');
      if (kind === 'other-account') document.cookie = 'nb_elevated=example-capability; Path=/';
      const resume = vi.fn();
      const hook = renderHook(() => useElevationReturn('7', resume), { wrapper });
      await waitFor(() => expect(sessionStorage.length).toBe(0));
      expect(resume).not.toHaveBeenCalled();
      hook.unmount();
      client.clear();
    },
  );
  it('guards a late download after unmount', async () => {
    const { client, wrapper } = fixture();
    writePendingElevation('export', '7');
    document.cookie = 'nb_elevated=example-capability; Path=/';
    let context!: OperationContext;
    const resume = vi.fn((_intent: ElevationIntent, _token: string, current: OperationContext) => {
      context = current;
    });
    const hook = renderHook(() => useElevationReturn('7', resume), { wrapper });
    await waitFor(() => expect(resume).toHaveBeenCalledOnce());
    hook.unmount();
    const download = vi.fn();
    expect(() => context.commit(download)).toThrow();
    expect(download).not.toHaveBeenCalled();
    client.clear();
  });
});
