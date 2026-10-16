import { act, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { renderWithProviders, scanQueryClientForTokens } from '../../../test/unit/support';
import { managedUserFixture } from '../../../test/fixtures/managedUsers';
import {
  beginManagementSessionRequest,
  noteManagementSessionSuccess,
} from '@shared/charityManagement';
import { UserMfaReset } from './UserMfaReset';

const json = (value: unknown) =>
  new Response(JSON.stringify(value), { headers: { 'content-type': 'application/json' } });
async function setup() {
  const view = await renderWithProviders(<span />, { station: 'admin' });
  act(() => {
    const session = { admin: { username: 'fixture-admin' } };
    noteManagementSessionSuccess(
      view.queryClient,
      'admin',
      session,
      beginManagementSessionRequest(view.queryClient, 'admin'),
    );
    view.queryClient.setQueryData(['admin', 'session'], session);
  });
  view.rerender(<UserMfaReset user={managedUserFixture()} />);
  await screen.findByRole('button', { name: 'Reset authenticator' });
  return view;
}

describe('administrator authenticator recovery', () => {
  it('keeps fresh elevation material out of caches and sends the reset only after confirmation', async () => {
    const requests: { path: string; token: string | null }[] = [];
    let enrolled = true;
    vi.stubGlobal(
      'fetch',
      vi.fn(async (path: string, options?: RequestInit) => {
        requests.push({ path, token: new Headers(options?.headers).get('X-Elevated-Token') });
        if (path.endsWith('/elevate'))
          return json({ token: 'synthetic-elevated-capability', expires_at: 1800000000 });
        if (path.endsWith('/reset')) {
          enrolled = false;
          return json({ enrolled: false });
        }
        return json({ enrolled });
      }),
    );
    const view = await setup();
    await view.user.click(screen.getByRole('button', { name: 'Reset authenticator' }));
    expect(requests.filter((request) => request.path.endsWith('/reset'))).toHaveLength(0);
    await view.user.type(screen.getByLabelText('Administrator password'), 'synthetic-password');
    await view.user.click(screen.getByRole('button', { name: 'Reset and sign out' }));
    await screen.findByText(
      'Authenticator reset. All browser sessions were signed out. The user must sign in again.',
    );
    expect(requests.filter((request) => request.path.endsWith('/reset'))).toEqual([
      { path: '/admin/api/users/7/mfa/reset', token: 'synthetic-elevated-capability' },
    ]);
    expect(
      scanQueryClientForTokens(view.queryClient, [
        'synthetic-elevated-capability',
        'synthetic-password',
      ]).hitSurfaces,
    ).toEqual([]);
    vi.unstubAllGlobals();
  });

  it('does not reset after the selected user detail unmounts during elevation', async () => {
    let finish!: (response: Response) => void;
    const fetch = vi.fn(async (path: string) =>
      path.endsWith('/elevate')
        ? new Promise<Response>((done) => {
            finish = done;
          })
        : json({ enrolled: true }),
    );
    vi.stubGlobal('fetch', fetch);
    const view = await setup();
    await view.user.click(screen.getByRole('button', { name: 'Reset authenticator' }));
    await view.user.type(screen.getByLabelText('Administrator password'), 'synthetic-password');
    await view.user.click(screen.getByRole('button', { name: 'Reset and sign out' }));
    await waitFor(() => expect(finish).toBeDefined());
    view.rerender(<span />);
    await act(async () => {
      finish(json({ token: 'synthetic-elevated-capability', expires_at: 1800000000 }));
    });
    expect(fetch.mock.calls.some(([path]) => path.endsWith('/reset'))).toBe(false);
    vi.unstubAllGlobals();
  });
});
