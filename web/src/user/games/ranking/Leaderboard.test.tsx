import { screen } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../test/unit/support';
import { Leaderboard } from './Leaderboard';

const api = vi.hoisted(() => ({ loadRanking: vi.fn() }));
vi.mock('./api', async (original) => ({
  ...(await original<typeof import('./api')>()),
  ...api,
}));
vi.mock('../../data', () => ({
  useUserSession: () => ({ data: { user: { id: '1', charity_profile_public: false } } }),
  userKeys: { session: ['user', 'session'], me: ['user', 'me'] },
}));

it.each(['1', '2'])(
  'offers charity pagination only when another page exists (%s pages)',
  async (pages) => {
    api.loadRanking.mockResolvedValue({
      asOf: 1800000000,
      statisticsStart: 1700000000,
      window: 'history',
      rows: [],
      me: null,
      rebuildStatus: null,
      pagination: {
        page: '1',
        total_pages: pages,
        total_items: pages === '1' ? '0' : '21',
        page_size: 20,
      },
    });
    const view = await renderWithProviders(<Leaderboard board="charity" />, {
      station: 'user',
      locale: 'en',
    });
    await screen.findByText('No qualifying rankings yet.');
    if (pages === '1') {
      expect(
        screen.queryByRole('navigation', { name: 'Charity leaderboard pages' }),
      ).not.toBeInTheDocument();
    } else {
      await view.user.click(screen.getByRole('button', { name: 'Next' }));
      expect(api.loadRanking).toHaveBeenLastCalledWith(
        'charity',
        'history',
        '2',
        expect.any(AbortSignal),
      );
    }
  },
);
