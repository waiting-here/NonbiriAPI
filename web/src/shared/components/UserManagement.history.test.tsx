import { screen, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../test/unit/support';
import { normalizeDeletedAccount } from '@shared/operations/managedUsers';
import { DeletedAccountCard } from './DeletedAccountCard';
import { UserManagement } from './UserManagement';

const deleted = {
  record_id: '42',
  former_user_id: '7',
  discord_id: '123456789012345678',
  snapshot_version: 1,
  registered_at: null,
  deleted_at: 1800000000,
  effective_level: null,
  ban: { state: 'unknown', active_at_deletion: null, reason: null, until: null },
  charity_pause: { state: 'unknown', active_at_deletion: null, reason: null, until: null },
  source: 'unknown',
  actor_user_id: null,
  blacklist_action: 'unknown',
  blacklist_reason_codes: [],
  general_balance: '-2',
  game_balance: null,
  donation_credit: null,
  sketch_paper: null,
  sketch_brush: null,
};

afterEach(() => vi.unstubAllGlobals());

it('shows a former account to a steward without management actions or administrator abort data', async () => {
  const paths: string[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = new URL(
        input instanceof Request ? input.url : String(input),
        window.location.origin,
      );
      paths.push(url.pathname);
      if (url.pathname === '/api/steward/users') {
        expect(url.searchParams.get('account_state')).toBe('all');
        return Response.json({
          data: [{ account_state: 'deleted', deleted }],
          next_cursor: null,
          pagination: { page: '1', page_size: 20, total_items: '1', total_pages: '1' },
        });
      }
      if (url.pathname === '/api/steward/users/deleted/42') return Response.json(deleted);
      throw new Error(`Unexpected request: ${url.pathname}`);
    }),
  );
  const view = await renderWithProviders(
    <UserManagement role="steward" account="9" scopeReady sessionError={null} />,
    { station: 'user', role: 'level6', route: '/steward?tab=users', locale: 'en' },
  );
  await screen.findByRole('button', { name: 'View' });
  await view.user.click(screen.getByRole('button', { name: 'View' }));
  await screen.findByRole('heading', { name: 'Deleted account' });
  expect(screen.getByText('Former user ID').nextElementSibling).toHaveTextContent('7');
  expect(screen.getAllByText('Unknown').length).toBeGreaterThan(0);
  expect(screen.queryByRole('button', { name: 'Ban' })).toBeNull();
  expect(screen.queryByRole('button', { name: 'Unban' })).toBeNull();
  expect(screen.queryByRole('button', { name: 'Delete' })).toBeNull();
  await waitFor(() => expect(paths).toContain('/api/steward/users/deleted/42'));
  expect(paths).not.toContain('/admin/api/users/deletion-duel-aborts');
});

it('links the exact deletion alert with the administrator list state but hides it from a steward', async () => {
  const account = normalizeDeletedAccount({ ...deleted, discord_id: null, alert_id: '77' });
  const admin = await renderWithProviders(
    <DeletedAccountCard account={account} role="admin" onClose={() => undefined} />,
    {
      station: 'admin',
      role: 'admin',
      locale: 'en',
      route: '/users?account_state=deleted&page=3&page_size=50&deleted=42',
    },
  );
  const target = screen.getByRole('link', { name: 'Related alert' });
  const destination = new URL(target.getAttribute('href')!, window.location.origin);
  expect(destination.pathname).toBe('/alerts');
  expect(destination.searchParams.get('alert_id')).toBe('77');
  expect(destination.searchParams.get('return_to')).toBe(
    '/users?account_state=deleted&page=3&page_size=50&deleted=42',
  );
  admin.unmount();
  await renderWithProviders(
    <DeletedAccountCard account={account} role="steward" onClose={() => undefined} />,
    {
      station: 'user',
      role: 'level6',
      locale: 'en',
      route: '/steward?tab=users&deleted=42',
    },
  );
  expect(screen.queryByRole('link', { name: 'Related alert' })).toBeNull();
});
