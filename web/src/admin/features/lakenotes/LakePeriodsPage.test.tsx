import { screen } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../test/unit/support';
import { LakePeriodsPage } from './LakePeriodsPage';
const api = vi.hoisted(() => ({ getPeriods: vi.fn() }));
vi.mock('@shared/lakenotes/api', async (original) => ({
  ...(await original<typeof import('@shared/lakenotes/api')>()),
  ...api,
}));
vi.mock('../../data', () => ({
  useAdminSession: () => ({
    data: { admin: { username: 'admin-a' } },
    isPending: false,
    isFetching: false,
    error: null,
  }),
}));
it('shows historical fees without period editing or activation', async () => {
  api.getPeriods.mockResolvedValue({
    items: [
      {
        id: 'past',
        name: 'Morning lake',
        status: 'published',
        starts_at: 1800000000,
        ends_at: 1800003600,
        entry_fee_milli: '2500',
      },
    ],
    page: 1,
    has_more: false,
  });
  await renderWithProviders(<LakePeriodsPage />, { station: 'admin', role: 'admin', locale: 'en' });
  expect(await screen.findByRole('heading', { name: 'Morning lake' })).toBeInTheDocument();
  expect(screen.getByRole('link', { name: 'Back to game settings' })).toHaveAttribute(
    'href',
    '/games',
  );
  expect(screen.queryByRole('textbox')).not.toBeInTheDocument();
  expect(screen.getAllByRole('button').length).toBe(2);
});
