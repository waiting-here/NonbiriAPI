import { screen } from '@testing-library/react';
import { expect, it } from 'vitest';
import { renderWithProviders } from '../../../../test/unit/support';
import { LogOriginIdentity } from './LogOriginIdentity';

it.each(['admin', 'steward'] as const)(
  'links a retained deleted identity to its history record for %s',
  async (role) => {
    await renderWithProviders(
      <LogOriginIdentity
        role={role}
        value={{
          origin_user_id: '7',
          origin_discord_id: '123456789012345678',
          origin_deleted: true,
          origin_unknown: false,
          history_record_id: '19',
        }}
      />,
      { station: role === 'admin' ? 'admin' : 'user' },
    );
    expect(screen.getByText('Original account: 7')).toBeVisible();
    const link = screen.getByRole('link', { name: 'View deletion history' });
    const destination = new URL(link.getAttribute('href')!, 'https://example.test');
    expect(destination.pathname).toBe(role === 'admin' ? '/users' : '/steward');
    expect(destination.searchParams.get('deleted')).toBe('19');
    expect(destination.searchParams.get('user')).toBeNull();
    if (role === 'steward') expect(destination.searchParams.get('tab')).toBe('users');
  },
);

it('shows missing historical facts without inventing an account link', async () => {
  await renderWithProviders(
    <LogOriginIdentity
      role="admin"
      value={{
        origin_user_id: null,
        origin_discord_id: null,
        origin_deleted: true,
        origin_unknown: true,
        history_record_id: null,
      }}
    />,
    { station: 'admin' },
  );
  expect(screen.queryByRole('link')).toBeNull();
  expect(screen.getByText(/Original account: Unknown/)).toBeVisible();
  expect(screen.getByText(/Some original identity facts are unavailable/)).toBeVisible();
});
