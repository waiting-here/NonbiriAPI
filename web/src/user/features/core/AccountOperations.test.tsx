import { useLayoutEffect, type ComponentProps } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { act, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { renderWithProviders, assertNoSensitiveQueryCache } from '../../../../test/unit/support';
import { AccountLifecyclePanel } from './AccountWorkspace';
import { coreKeys } from './queries';
import { downloadAccountExport, writePendingElevation } from './sensitive';
import type { AccountLifecycleAdapter } from './types';

const navigate = vi.hoisted(() => vi.fn());
vi.mock('react-router', async (original) => ({
  ...(await original<typeof import('react-router')>()),
  useNavigate: () => navigate,
}));
vi.mock('./sensitive', async (original) => ({
  ...(await original<typeof import('./sensitive')>()),
  downloadAccountExport: vi.fn(),
}));
const session = (id = '1') => ({
  user: { id, username: 'account-' + id, level: 2, effective_level: 2 },
});
function Fixture(props: ComponentProps<typeof AccountLifecyclePanel>) {
  const client = useQueryClient();
  useLayoutEffect(() => {
    client.setQueryData(coreKeys.session, session(props.accountId));
  }, [client, props.accountId]);
  return <AccountLifecyclePanel {...props} />;
}
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((settle) => {
    resolve = settle;
  });
  return { promise, resolve };
}
const attachment = { blob: new Blob(['{}']), schemaVersion: 11 as const };
function adapter(
  exporter: AccountLifecycleAdapter['exportAccount'] = vi.fn(async () => attachment),
): AccountLifecycleAdapter {
  return {
    capabilities: { exportAccount: true, deleteAccount: true },
    beginElevation: vi.fn(async () => 'https://identity.example.test/verify'),
    exportAccount: exporter,
    deleteAccount: vi.fn(async () => undefined),
    readAccountAuthority: vi.fn(async () => 'active' as const),
  };
}
function returned(intent: 'export' | 'delete', id = '1') {
  writePendingElevation(intent, id);
  document.cookie = 'nb_elevated=synthetic-capability; Path=/; SameSite=Lax';
}
describe('account operation consumers', () => {
  it('automatically downloads the original export once after verified return without another confirmation', async () => {
    returned('export');
    const actions = adapter();
    const rendered = await renderWithProviders(<Fixture accountId="1" adapter={actions} />, {
      station: 'user',
      locale: 'en',
    });
    await waitFor(() => expect(downloadAccountExport).toHaveBeenCalledOnce());
    expect(actions.exportAccount).toHaveBeenCalledWith({
      accountId: '1',
      elevatedToken: 'synthetic-capability',
      signal: expect.any(AbortSignal),
    });
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument();
    expect(document.cookie).not.toContain('synthetic-capability');
    expect(sessionStorage.getItem('nb.pending.elevation')).toBeNull();
    expect(
      assertNoSensitiveQueryCache(rendered.queryClient, ['synthetic-capability']).hitSurfaces,
    ).toEqual([]);
  });

  it.each(['cancel', 'account', 'unmount'] as const)(
    'does not download a late export after %s',
    async (boundary) => {
      returned('export');
      const response = deferred<typeof attachment>();
      let signal: AbortSignal | undefined;
      const exporter = vi.fn<AccountLifecycleAdapter['exportAccount']>((input) => {
        signal = input.signal;
        return response.promise;
      });
      const rendered = await renderWithProviders(
        <Fixture accountId="1" adapter={adapter(exporter)} />,
        { station: 'user', locale: 'en' },
      );
      await waitFor(() => expect(exporter).toHaveBeenCalledOnce());
      if (boundary === 'cancel')
        await rendered.user.click(screen.getByRole('button', { name: 'Cancel' }));
      if (boundary === 'account')
        act(() => rendered.queryClient.setQueryData(coreKeys.session, session('2')));
      if (boundary === 'unmount') rendered.unmount();
      expect(signal?.aborted).toBe(true);
      await act(async () => {
        response.resolve(attachment);
        await response.promise;
      });
      expect(downloadAccountExport).not.toHaveBeenCalled();
      if (boundary === 'account')
        expect(rendered.queryClient.getQueryData(coreKeys.session)).toEqual(session('2'));
    },
  );

  it.each(['missing', 'other-account'] as const)(
    'does not export after a %s capability return',
    async (kind) => {
      if (kind === 'other-account') returned('export', '2');
      else writePendingElevation('export', '1');
      const actions = adapter();
      await renderWithProviders(<Fixture accountId="1" adapter={actions} />, {
        station: 'user',
        locale: 'en',
      });
      await waitFor(() => expect(sessionStorage.getItem('nb.pending.elevation')).toBeNull());
      expect(actions.exportAccount).not.toHaveBeenCalled();
      expect(downloadAccountExport).not.toHaveBeenCalled();
    },
  );

  it('cancels the sole deletion confirmation without sending a deletion', async () => {
    returned('delete');
    const actions = adapter();
    await renderWithProviders(<Fixture accountId="1" adapter={actions} />, {
      station: 'user',
      locale: 'en',
    });
    const dialog = await screen.findByRole('alertdialog');
    expect(within(dialog).queryByRole('textbox')).not.toBeInTheDocument();
    await screen.findByRole('button', { name: 'Cancel' });
    await act(async () => {
      within(dialog).getByRole('button', { name: 'Cancel' }).click();
    });
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument();
    expect(actions.deleteAccount).not.toHaveBeenCalled();
    expect(document.cookie).not.toContain('synthetic-capability');
  });
  it('closes a verified deletion confirmation when the account session changes', async () => {
    returned('delete');
    const actions = adapter();
    const rendered = await renderWithProviders(<Fixture accountId="1" adapter={actions} />, {
      station: 'user',
      locale: 'en',
    });
    await screen.findByRole('alertdialog');
    act(() => rendered.queryClient.setQueryData(coreKeys.session, session('2')));
    await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument());
    expect(actions.deleteAccount).not.toHaveBeenCalled();
    expect(rendered.queryClient.getQueryData(coreKeys.session)).toEqual(session('2'));
    expect(navigate).not.toHaveBeenCalled();
    expect(
      assertNoSensitiveQueryCache(rendered.queryClient, ['synthetic-capability']).hitSurfaces,
    ).toEqual([]);
  });

  it('preserves the new account session and location after a late deletion succeeds', async () => {
    returned('delete');
    const response = deferred<void>();
    const actions = adapter();
    actions.deleteAccount = vi.fn(() => response.promise);
    const rendered = await renderWithProviders(<Fixture accountId="1" adapter={actions} />, {
      station: 'user',
      locale: 'en',
    });
    const dialog = await screen.findByRole('alertdialog');
    await rendered.user.click(
      within(dialog).getByRole('button', { name: 'Permanently delete account' }),
    );
    await waitFor(() => expect(actions.deleteAccount).toHaveBeenCalledOnce());
    expect(actions.deleteAccount).toHaveBeenCalledWith({
      accountId: '1',
      elevatedToken: 'synthetic-capability',
      confirmation: 'DELETE',
      signal: expect.any(AbortSignal),
    });
    act(() => rendered.queryClient.setQueryData(coreKeys.session, session('2')));
    await act(async () => {
      response.resolve();
      await response.promise;
    });
    expect(rendered.queryClient.getQueryData(coreKeys.session)).toEqual(session('2'));
    expect(navigate).not.toHaveBeenCalled();
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument();
  });
});
