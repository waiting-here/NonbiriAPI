import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../test/unit/support';
import {
  attendanceAccount,
  attendanceAssets,
  attendancePage,
  attendancePeriod,
} from '../../../../test/unit/attendance';
import { ApiError } from '@shared/query/http';
import { attendanceKeys, type SignInResult } from '@shared/attendance/api';
import { AttendanceSection } from './AttendanceSection';
import { creditHistoryKeys } from '../credits/queries';
import { limitedActivityKeys } from '../limitedactivities/queries';

const api = vi.hoisted(() => ({ list: vi.fn(), sign: vi.fn() }));
vi.mock('@shared/attendance/api', async (load) => ({
  ...(await load<typeof import('@shared/attendance/api')>()),
  listAttendance: api.list,
  signIn: api.sign,
}));
vi.mock('@shared/rewards/api', async (load) => ({
  ...(await load<typeof import('@shared/rewards/api')>()),
  getAssets: async () => attendanceAssets,
}));
vi.mock('../economy/queries', () => ({
  economySessionRequest: (_client: unknown, request: () => Promise<unknown>) => request(),
}));
let page = attendancePage();
beforeEach(() => {
  page = attendancePage();
  api.list.mockReset().mockImplementation(async () => structuredClone(page));
  api.sign.mockReset();
});
function result(patch: Partial<SignInResult> = {}): SignInResult {
  return {
    period: page.items[0],
    period_id: page.items[0].id,
    site_date: '2026-11-03',
    signed_at: 1_794_000_000,
    already_signed: false,
    awards: [],
    ...patch,
  };
}

it('keeps selected milestones and the next reward visible across campaigns and expands in day order', async () => {
  const tiers = Array.from({ length: 7 }, (_, index) => ({
    days: index + 1,
    featured: index === 2 || index === 6,
    awarded: index < 1,
    rewards: [{ asset: 'game', quantity: '10' }],
  }));
  page = attendancePage([
    attendancePeriod({ tiers, cover: { kind: 'collection', value: 'r', fit: 'contain' } }),
    attendancePeriod({ id: 'winter', title: 'Winter days' }),
  ]);
  const view = await renderWithProviders(<AttendanceSection account="7" />, { station: 'user' });
  const first = await screen.findByRole('region', { name: 'Autumn days' });
  const list = first.querySelector('ol')!;
  expect(first.querySelector('.attendance-cover img')).toHaveAttribute(
    'src',
    '/assets/gacha/groups/r.webp',
  );
  expect(first.querySelector('.attendance-cover')).toHaveAttribute('data-fit', 'contain');
  expect(within(list).getAllByText('Featured reward')).toHaveLength(2);
  expect(within(list).getByText('Day 2')).toBeVisible();
  expect(within(list).queryByText('Day 4')).not.toBeInTheDocument();
  await view.user.click(within(first).getByRole('button', { name: 'View all 7 milestones' }));
  expect(Array.from(list.children, (li) => li.querySelector('strong')?.textContent)).toEqual(
    tiers.map((tier) => 'Day ' + tier.days),
  );
  await view.user.click(within(first).getByRole('button', { name: 'Show fewer milestones' }));
  expect(list.children).toHaveLength(3);
});

it('shows every milestone for one campaign and restores the default artwork if an external cover fails', async () => {
  const tiers = Array.from({ length: 7 }, (_, index) => ({
    days: index + 1,
    featured: index === 6,
    awarded: false,
    rewards: [{ asset: 'game', quantity: '10' }],
  }));
  page = attendancePage([
    attendancePeriod({
      tiers,
      cover: {
        kind: 'external',
        value: 'https://images.example.test/portrait.jpg',
        fit: 'contain',
      },
    }),
  ]);
  await renderWithProviders(<AttendanceSection account="7" />, { station: 'user' });
  const first = await screen.findByRole('region', { name: 'Autumn days' });
  expect(first.querySelector('ol')?.children).toHaveLength(7);
  expect(within(first).getByText('Featured reward').closest('li')).toHaveTextContent('Day 7');
  const image = first.querySelector<HTMLImageElement>('.attendance-cover img')!;
  expect(image).toHaveAttribute('crossorigin', 'anonymous');
  expect(image).toHaveAttribute('referrerpolicy', 'no-referrer');
  fireEvent.error(image);
  expect(image).toHaveAttribute('src', '/assets/gacha/groups/a.webp');
  expect(image).not.toHaveAttribute('crossorigin');
  expect(within(first).getByRole('button', { name: 'Sign in today' })).toBeEnabled();
});

it('signs in to one period independently and shows automatic milestone rewards', async () => {
  page = attendancePage([
    attendancePeriod(),
    attendancePeriod({
      id: 'winter',
      title: 'Winter streak',
      mode: 'consecutive',
      current_streak: 1,
      best_streak: 7,
      tiers: [{ days: 7, rewards: [{ asset: 'game', quantity: '200' }], awarded: true }],
    }),
  ]);
  api.sign.mockImplementation(async () => {
    page.items[0] = {
      ...page.items[0],
      total_days: 7,
      signed_today: true,
      can_sign_in: false,
      tiers: page.items[0].tiers.map((tier) => ({ ...tier, awarded: tier.days === 7 })),
    };
    return result({
      awards: [
        {
          days: 7,
          awarded_at: 1_794_000_000,
          results: [{ asset: 'general', quantity: '100', granted: '100' }],
        },
      ],
    });
  });
  const view = await renderWithProviders(<AttendanceSection account="7" />, { station: 'user' });
  view.queryClient.setQueryData(['user', 'session'], attendanceAccount);
  const first = await screen.findByRole('region', { name: 'Autumn days' });
  const second = screen.getByRole('region', { name: 'Winter streak' });
  expect(within(second).getByText('Best streak: 7 days')).toBeVisible();
  expect(within(second).getByText('Awarded')).toBeVisible();
  await view.user.click(within(first).getByRole('button', { name: 'Sign in today' }));
  expect(await within(first).findByRole('button', { name: 'Signed in today' })).toBeDisabled();
  expect(within(second).getByRole('button', { name: 'Sign in today' })).toBeEnabled();
  expect(first.querySelector('.reward-number--grant')).toHaveTextContent('+100');
  expect(within(first).getByText('Not reached')).toBeVisible();
  expect(screen.queryByRole('button', { name: /Claim/ })).not.toBeInTheDocument();
  expect(api.sign.mock.calls[0].slice(0, 2)).toEqual(['autumn', '2026-11-03']);
});

it('dispatches a repeated click only once while another period remains usable', async () => {
  page = attendancePage([
    attendancePeriod(),
    attendancePeriod({ id: 'winter', title: 'Winter days' }),
  ]);
  api.sign.mockImplementation(() => new Promise(() => undefined));
  const view = await renderWithProviders(<AttendanceSection account="7" />, { station: 'user' });
  view.queryClient.setQueryData(['user', 'session'], attendanceAccount);
  const first = await screen.findByRole('region', { name: 'Autumn days' });
  const button = within(first).getByRole('button', { name: 'Sign in today' });
  fireEvent.click(button);
  fireEvent.click(button);
  await waitFor(() => expect(api.sign).toHaveBeenCalledTimes(1));
  await view.user.click(
    within(screen.getByRole('region', { name: 'Winter days' })).getByRole('button', {
      name: 'Sign in today',
    }),
  );
  await waitFor(() => expect(api.sign).toHaveBeenCalledTimes(2));
  view.unmount();
});

it('retries the original date and key after an uncertain sign-in across midnight', async () => {
  api.sign
    .mockRejectedValueOnce(new ApiError('network_error', 'Response lost', 0))
    .mockImplementation(async () => result({ already_signed: true }));
  const view = await renderWithProviders(<AttendanceSection account="7" />, { station: 'user' });
  view.queryClient.setQueryData(['user', 'session'], attendanceAccount);
  const historyKey = [...creditHistoryKeys.account('7'), { page: '1' }];
  const walletKey = limitedActivityKeys.wallet('7');
  const otherKey = [...creditHistoryKeys.account('8'), { page: '1' }];
  for (const key of [historyKey, walletKey, otherKey]) view.queryClient.setQueryData(key, {});
  await view.user.click(await screen.findByRole('button', { name: 'Sign in today' }));
  await screen.findByRole('button', { name: 'Retry this sign-in' });
  expect(view.queryClient.getQueryState(historyKey)?.isInvalidated).toBe(true);
  expect(view.queryClient.getQueryState(walletKey)?.isInvalidated).toBe(true);
  expect(view.queryClient.getQueryState(otherKey)?.isInvalidated).toBe(false);
  expect(api.sign).toHaveBeenCalledTimes(1);
  page.items[0] = { ...page.items[0], site_date: '2026-11-04' };
  view.queryClient.setQueriesData(
    { queryKey: attendanceKeys.root(false, '7') },
    structuredClone(page),
  );
  await view.user.click(screen.getByRole('button', { name: 'Retry this sign-in' }));
  await waitFor(() => expect(api.sign).toHaveBeenCalledTimes(2));
  expect(api.sign.mock.calls[0].slice(0, 3)).toEqual(api.sign.mock.calls[1].slice(0, 3));
  expect(api.sign.mock.calls[1][1]).toBe('2026-11-03');
  expect(await screen.findByText('Sign-in recorded for 2026-11-03')).toBeVisible();
});
