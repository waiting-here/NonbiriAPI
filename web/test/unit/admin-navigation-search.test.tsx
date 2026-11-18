import { screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { AdminNavigation } from '../../src/admin/layouts/AdminNavigation';
import { renderWithProviders } from './support';

vi.mock('@shared/components/DonationPendingBadge', () => ({ DonationPendingBadge: () => null }));

describe('finding a management page', () => {
  it('finds links inside collapsed groups, accepts full-width pasted paths, and restores the group on clear', async () => {
    localStorage.setItem('nb.admin.nav.collapsed', '["records"]');
    const follow = vi.fn();
    const view = await renderWithProviders(
      <AdminNavigation accountId="admin" onNavigate={follow} />,
      { station: 'admin', route: '/settings' },
    );
    const menu = within(screen.getByRole('navigation'));
    const search = menu.getByRole('textbox', { name: 'Find a page' });
    expect(menu.queryByRole('link', { name: 'Logs & usage' })).toBeNull();
    await view.user.click(search);
    await view.user.paste('／ＬＯＧＳ');
    expect(menu.getByRole('link', { name: 'Logs & usage' })).toHaveAttribute('href', '/logs');
    await view.user.keyboard('{Escape}');
    expect(search).toHaveValue('');
    expect(search).toHaveFocus();
    expect(menu.queryByRole('link', { name: 'Logs & usage' })).toBeNull();
    await view.user.type(search, 'unknown-page');
    expect(menu.getByRole('status')).toHaveTextContent(/no matching page/i);
    await view.user.click(menu.getByRole('button', { name: 'Clear page search' }));
    await view.user.type(search, '/logs{Enter}');
    expect(follow).toHaveBeenCalledTimes(1);
    expect(search).toHaveValue('');
  });
});
