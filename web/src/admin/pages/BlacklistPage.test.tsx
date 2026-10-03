import { screen, waitFor, within } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../test/unit/support';
import { installNativeDialog } from '../../../test/unit/nativeDialog';
import { BlacklistPage } from './BlacklistPage';
import { validDiscordID } from '../features/operations/blacklist';

installNativeDialog();
afterEach(() => vi.unstubAllGlobals());

it('validates Discord IDs without losing numeric precision', () => {
  for (const id of ['123456789012345678', '18446744073709551615'])
    expect(validDiscordID(id)).toBe(true);
  for (const id of ['0', '01', '1e18', '18446744073709551616', '123 456'])
    expect(validDiscordID(id)).toBe(false);
});

it.each([false, true])(
  'adds and removes an identity with idempotency keys (lost remove response: %s)',
  async (loseRemoveResponse) => {
    let listed = false;
    let removeAttempts = 0;
    const writes: { path: string; body: unknown; key: string | null }[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = new URL(
          input instanceof Request ? input.url : String(input),
          window.location.origin,
        );
        if (url.pathname === '/admin/api/session')
          return Response.json({ admin: { username: 'root' } });
        if (init?.method === 'POST') {
          writes.push({
            path: url.pathname,
            body: init.body ? JSON.parse(String(init.body)) : null,
            key: new Headers(init.headers).get('Idempotency-Key'),
          });
          if (url.pathname.endsWith('/remove') && loseRemoveResponse && removeAttempts++ === 0)
            return Response.json(
              { error: { code: 'unavailable', message: 'Response unavailable' } },
              { status: 503 },
            );
          listed = !url.pathname.endsWith('/remove');
          return new Response(null, { status: 204 });
        }
        if (url.pathname === '/admin/api/blacklist')
          return Response.json({
            data: listed
              ? [
                  {
                    discord_id: '123456789012345678',
                    reason: 'Policy violation',
                    created_at: 1800000000,
                    user_id: '42',
                  },
                ]
              : [],
            next_cursor: null,
            pagination: {
              page: '1',
              page_size: 20,
              total_items: listed ? '1' : '0',
              total_pages: '1',
            },
          });
        throw new Error('Unexpected request');
      }),
    );
    const rendered = await renderWithProviders(<BlacklistPage />, {
      station: 'admin',
      role: 'admin',
      route: '/blacklist',
    });
    await rendered.user.click(await screen.findByRole('button', { name: 'Add to blacklist' }));
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Add and permanently ban' })).toBeEnabled(),
    );
    await rendered.user.type(screen.getByLabelText('Discord ID'), '123456789012345678');
    await rendered.user.type(screen.getByLabelText('Reason'), 'Policy violation');
    await rendered.user.click(screen.getByRole('button', { name: 'Add and permanently ban' }));
    const dialog = await screen.findByRole('alertdialog');
    expect(dialog).toHaveTextContent('123456789012345678');
    expect(dialog).toHaveTextContent('Policy violation');
    expect(writes).toHaveLength(0);
    await rendered.user.click(
      within(dialog).getByRole('button', { name: 'Add and permanently ban' }),
    );
    expect(await screen.findByRole('link', { name: /#42/ })).toBeVisible();
    await rendered.user.click(screen.getByRole('button', { name: /123456789012345678/ }));
    await rendered.user.click(screen.getByRole('menuitem', { name: 'Remove from blacklist' }));
    if (loseRemoveResponse) {
      const retry = await screen.findByRole('button', { name: 'Retry' });
      expect(retry).toBeVisible();
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
      await rendered.user.click(retry);
    }
    expect(await screen.findByText(/Removed from the blacklist/)).toBeVisible();
    await waitFor(() =>
      expect(screen.queryByRole('link', { name: /#42/ })).not.toBeInTheDocument(),
    );
    expect(writes.map((entry) => entry.path)).toEqual([
      '/admin/api/blacklist',
      '/admin/api/blacklist/123456789012345678/remove',
      ...(loseRemoveResponse ? ['/admin/api/blacklist/123456789012345678/remove'] : []),
    ]);
    expect(writes[0]?.body).toEqual({
      discord_id: '123456789012345678',
      reason: 'Policy violation',
    });
    expect(writes.every((entry) => /^[A-Za-z0-9_-]{22}$/.test(entry.key ?? ''))).toBe(true);
    expect(writes[0]?.key).not.toBe(writes[1]?.key);
    if (loseRemoveResponse) expect(writes[2]).toEqual(writes[1]);
  },
);
