import { screen } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../test/unit/support';
import { CharityPage } from './CharityPage';

vi.mock('../data', () => ({
  useUserSession: () => ({ data: { user: { id: '7', effective_level: 1, donation_credit: '0' } } }),
}));
vi.mock('@shared/query/publicConfig', () => ({ usePublicConfig: () => ({ data: {} }) }));
vi.mock('../features/economy/queries', () => ({
  useCharityCapability: () => ({ data: { donationIntake: 'open' } }),
  useDonation: vi.fn(),
}));
vi.mock('../features/economy/CharityCatalogPanel', () => ({
  CharityCatalogPanel: () => <input aria-label="Catalog search" />,
}));
vi.mock('../features/economy/OwnerDonationsPanel', () => ({
  OwnerDonationKeys: () => null,
  OwnerDonationsPanel: () => <p>Owned donation list</p>,
}));
vi.mock('../features/economy/CharityPanels', () => ({
  CharitySafetyNotice: () => <p>Donation notice</p>,
  DonationCard: () => null,
  DonationIntakePanel: () => null,
  DonationComposer: () => <input aria-label="Donation draft" />,
}));
vi.mock('../games/ranking/Leaderboard', () => ({
  Leaderboard: ({ enabled = true }: { enabled?: boolean }) => (
    <p>{enabled ? 'Charity board' : 'Board paused'}</p>
  ),
}));

it('shows only the selected charity section, keeps the board visible, and preserves inputs', async () => {
  const view = await renderWithProviders(<CharityPage />, { station: 'user', route: '/charity' });
  const catalog = screen.getByRole('textbox', { name: 'Catalog search' });
  await view.user.type(catalog, 'selected model');
  expect(screen.getByText('Charity board')).toBeVisible();
  await view.user.click(screen.getByRole('tab', { name: 'My donations' }));
  expect(catalog).not.toBeVisible();
  expect(catalog.closest('[hidden]')).not.toHaveClass('economy-model-workspace');
  expect(screen.getByText('Owned donation list')).toBeVisible();
  expect(screen.getByText('Charity board')).toBeVisible();
  await view.user.click(screen.getByRole('tab', { name: 'Donate my keys' }));
  const draft = screen.getByRole('textbox', { name: 'Donation draft' });
  await view.user.type(draft, 'My pending note');
  expect(screen.getByText('Owned donation list')).not.toBeVisible();
  expect(screen.getByText('Charity board')).toBeVisible();
  await view.user.click(screen.getByRole('tab', { name: 'Shared models' }));
  expect(catalog).toHaveValue('selected model');
  expect(draft).not.toBeVisible();
  await view.user.click(screen.getByRole('tab', { name: 'Donate my keys' }));
  expect(draft).toHaveValue('My pending note');
  expect(screen.getByText('Charity board')).toBeVisible();
});
