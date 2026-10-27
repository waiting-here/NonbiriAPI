import type { ReactNode } from 'react';
import { screen, waitFor } from '@testing-library/react';
import { useLocation, useNavigate } from 'react-router';
import { describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../test/unit/support';
import { GachaSettingsPage } from '../../pages/GachaSettingsPage';

vi.mock('@shared/artwork/policy', () => ({
  ConfiguredArtworkPolicy: ({ children }: { children: ReactNode }) => children,
}));
vi.mock('@shared/components/TimeContext', () => ({ TimeContextNotice: () => null }));
vi.mock('../../data', () => ({
  useAdminSession: () => ({ data: { admin: { username: 'admin' } } }),
}));
vi.mock('../../../user/activities/gacha/api', () => ({
  getGachaConfiguration: async () => ({ pools: [], presets: [], revision: 1 }),
  saveGachaConfiguration: vi.fn(),
}));
vi.mock('./GachaPoolStatistics', () => ({
  GachaPoolStatistics: () => <h2>Pool statistics view</h2>,
}));
vi.mock('./GachaShopStatistics', () => ({
  GachaShopStatistics: () => <h2>Shop statistics view</h2>,
}));
function Navigation() {
  const location = useLocation();
  const navigate = useNavigate();
  return (
    <>
      <output data-testid="location">{location.pathname + location.search}</output>
      <button onClick={() => navigate(-1)}>History back</button>
      <button onClick={() => navigate(1)}>History forward</button>
      <GachaSettingsPage />
    </>
  );
}
describe('gacha administrator view navigation', () => {
  it('restores a statistics view from the URL and keeps both tables state through back and forward', async () => {
    const route =
      '/activities/gacha?gacha_view=statistics&stats_pool=permanent&stats_cards_page=2&stats_shop_q=silver';
    const view = await renderWithProviders(<Navigation />, {
      station: 'admin',
      locale: 'en',
      role: 'admin',
      route,
    });
    await screen.findByRole('heading', { name: 'Pool statistics view' });
    expect(screen.getByRole('tab', { name: 'Draw statistics' })).toHaveAttribute(
      'aria-selected',
      'true',
    );
    await view.user.click(screen.getByRole('tab', { name: 'Shop card statistics' }));
    await screen.findByRole('heading', { name: 'Shop statistics view' });
    expect(screen.getByTestId('location')).toHaveTextContent('gacha_view=shop-statistics');
    expect(screen.getByTestId('location')).toHaveTextContent('stats_cards_page=2');
    expect(screen.getByTestId('location')).toHaveTextContent('stats_shop_q=silver');
    await view.user.click(screen.getByRole('button', { name: 'History back' }));
    await screen.findByRole('heading', { name: 'Pool statistics view' });
    await view.user.click(screen.getByRole('button', { name: 'History forward' }));
    await screen.findByRole('heading', { name: 'Shop statistics view' });
    const savedRoute = screen.getByTestId('location').textContent!;
    view.unmount();
    await renderWithProviders(<Navigation />, {
      station: 'admin',
      locale: 'en',
      role: 'admin',
      route: savedRoute,
    });
    await screen.findByRole('heading', { name: 'Shop statistics view' });
    expect(screen.getByRole('tab', { name: 'Shop card statistics' })).toHaveAttribute(
      'aria-selected',
      'true',
    );
  });
  it('uses the default pools view for an unknown URL view without losing other state', async () => {
    await renderWithProviders(<Navigation />, {
      station: 'admin',
      locale: 'en',
      role: 'admin',
      route: '/activities/gacha?gacha_view=unknown&stats_shop_q=kept',
    });
    await waitFor(() =>
      expect(screen.getByRole('tab', { name: 'Limited pools' })).toHaveAttribute(
        'aria-selected',
        'true',
      ),
    );
    expect(screen.getByTestId('location')).toHaveTextContent('stats_shop_q=kept');
  });
});
