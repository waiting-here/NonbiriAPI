import { fireEvent, screen, waitFor } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../test/unit/support';
import { ApiError } from '@shared/query/http';
import { RewardsPage } from './RewardsPage';

const api = vi.hoisted(() => ({ redeem: vi.fn() }));
const account = { user: { id: '7', username: 'reward-reader', effective_level: 1 } };
vi.mock('../data', () => ({
  useUserSession: () => ({ data: account, isPending: false, error: null }),
}));
vi.mock('@shared/rewards/api', async (load) => ({
  ...(await load<typeof import('@shared/rewards/api')>()),
  getAssets: async () => [],
  getReceipts: async () => ({ items: [], next_cursor: '' }),
  redeemReward: api.redeem,
}));
beforeEach(() => {
  api.redeem.mockReset();
});

it('reuses the uncertain submission key, then starts a new explicit submission after a known refusal', async () => {
  api.redeem
    .mockRejectedValueOnce(new ApiError('network_error', 'Response lost', 0))
    .mockResolvedValue({ rejection: 'conditions' });
  const view = await renderWithProviders(<RewardsPage />, { station: 'user' });
  view.queryClient.setQueryData(['user', 'session'], account);
  const input = screen.getByRole('textbox');
  await view.user.type(input, 'fixture-reward');
  await view.user.click(screen.getByRole('button', { name: 'Claim rewards' }));
  await view.user.click(await screen.findByRole('button', { name: 'Retry' }));
  expect(api.redeem).toHaveBeenCalledTimes(2);
  expect(api.redeem.mock.calls[0]).toEqual(api.redeem.mock.calls[1]);
  await view.user.click(await screen.findByRole('button', { name: 'Claim rewards' }));
  await waitFor(() => expect(api.redeem).toHaveBeenCalledTimes(3));
  expect(api.redeem.mock.calls[2][1]).not.toBe(api.redeem.mock.calls[1][1]);
  expect(view.queryClient.getQueryData(['user', 'session'])).toEqual(account);
});

it('dispatches only once when the form is submitted twice before rendering busy feedback', async () => {
  api.redeem.mockImplementation(() => new Promise(() => undefined));
  const view = await renderWithProviders(<RewardsPage />, { station: 'user' });
  view.queryClient.setQueryData(['user', 'session'], account);
  await view.user.type(screen.getByRole('textbox'), 'fixture-reward');
  const form = screen.getByRole('button', { name: 'Claim rewards' }).closest('form')!;
  fireEvent.submit(form);
  fireEvent.submit(form);
  expect(api.redeem).toHaveBeenCalledTimes(1);
  expect(screen.getByRole('textbox')).toBeDisabled();
  view.unmount();
});

it('shows the ban expiry and refreshes authoritative session state', async () => {
  api.redeem.mockResolvedValue({ rejection: 'banned', banned_until: 1_800_086_400 });
  const view = await renderWithProviders(<RewardsPage />, { station: 'user' });
  view.queryClient.setQueryData(['user', 'session'], account);
  const invalidate = vi.spyOn(view.queryClient, 'invalidateQueries');
  await view.user.type(screen.getByRole('textbox'), 'fixture-reward');
  await view.user.click(screen.getByRole('button', { name: 'Claim rewards' }));
  expect(
    await screen.findByText(/Repeated invalid code submissions have restricted your account until/),
  ).toBeVisible();
  expect(invalidate).toHaveBeenCalledWith({ queryKey: ['user', 'session'] });
  expect(api.redeem).toHaveBeenCalledTimes(1);
});
