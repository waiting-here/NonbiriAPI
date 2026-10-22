import { screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../test/unit/support';
import { DonationUsesPanel } from './DonationUsesPanel';
const mocks = vi.hoisted(() => ({ get: vi.fn(), review: vi.fn() }));
vi.mock('@shared/operations/donationUses', async (original) => ({
  ...(await original<typeof import('@shared/operations/donationUses')>()),
  getDonationUses: mocks.get,
  reviewDonationUses: mocks.review,
}));
const version = (number: string, status: 'approved' | 'pending') => ({
  credential_id: '8',
  credential_version: number,
  grant_revision: number,
  status,
  review_note: '',
  reviewed_at: null,
  expected_review_revision: 'body-' + number,
  discovery_source_id: null,
  discovery_source_revision: null,
  display_head: 'head-' + number,
  display_tail: 'tail',
  review: { required: true, revision: 'body-' + number, material_available: true },
  discovery: null,
});
const row = (id: string, revision: string) => ({
  id,
  donation_id: '3',
  donation_key_id: '4',
  endpoint_key_id: '5',
  native_service: id === '6' ? 'chat_completions' : 'responses',
  enabled: true,
  discovery_enabled: false,
  revision,
  approved: version('1', 'approved'),
  pending: version('2', 'pending'),
  latest: version('2', 'pending'),
  health: null,
  capabilities: {
    can_review: true,
    can_manage: false,
    can_reset_failures: false,
    can_discover: false,
    can_bind: true,
    unavailable_reason: null,
  },
});
afterEach(() => vi.clearAllMocks());
describe('per-use review recovery', () => {
  it('keeps the failed selection, shows both versions, and requires explicit adoption of a refreshed revision', async () => {
    let revision = '9';
    mocks.get.mockImplementation(async () => ({
      data: [row('6', '9'), row('7', revision)],
      next_cursor: null,
      pagination: { page: '1', page_size: 20, total_items: '2', total_pages: '1' },
    }));
    mocks.review
      .mockImplementationOnce(async () => {
        revision = '10';
        return [
          { use_id: '6', ok: true },
          { use_id: '7', ok: false, error: { code: 'conflict', message: 'Version changed' } },
        ];
      })
      .mockResolvedValue([{ use_id: '7', ok: true }]);
    const view = await renderWithProviders(
      <DonationUsesPanel role="steward" accountId="5" donationId="3" />,
      { station: 'user', role: 'level5', route: '/charity' },
    );
    view.queryClient.setQueryData(['user', 'session'], {
      user: { id: '5', username: 'reviewer', effective_level: 5 },
    });
    for (const name of ['Chat completions', 'Responses'])
      await view.user.click(await screen.findByText(name, { exact: true }));
    for (const checkbox of screen.getAllByRole('checkbox', { name: 'Select use for review' }))
      await view.user.click(checkbox);
    expect(screen.getAllByText('head-1…tail · Version 1')).toHaveLength(2);
    expect(screen.getAllByText('head-2…tail · Version 2')).toHaveLength(2);
    await view.user.click(screen.getByRole('button', { name: 'Approve selected uses' }));
    await waitFor(() => expect(mocks.review).toHaveBeenCalledTimes(1));
    await screen.findByText(/Version changed/);
    const checked = screen.getAllByRole('checkbox', { name: 'Select use for review' });
    expect(checked[0]).not.toBeChecked();
    expect(checked[1]).toBeChecked();
    await waitFor(() => expect(mocks.get).toHaveBeenCalledTimes(2));
    const adopt = screen.getByRole('button', { name: 'Continue with current revision' });
    await view.user.click(adopt);
    await view.user.click(screen.getByRole('button', { name: 'Approve selected uses' }));
    await waitFor(() => expect(mocks.review).toHaveBeenCalledTimes(2));
    expect(mocks.review.mock.calls[1]?.[2]).toEqual([
      {
        use_id: '7',
        expected_revision: '10',
        expected_review_revision: 'body-2',
        decision: 'approve',
        review_note: '',
      },
    ]);
    expect(mocks.review.mock.calls[1]?.[3]).not.toBe(mocks.review.mock.calls[0]?.[3]);
  });
});

it('reopens a current use confirmation after conflict and keeps uncertain recovery inside the dialog', async () => {
  let revision = '9';
  let complete = false;
  const writes: { body: unknown; key: string | null }[] = [];
  mocks.get.mockImplementation(async () => ({
    data: [
      {
        ...row('6', revision),
        endpoint_key_id: null,
        pending: complete ? null : version('2', 'pending'),
        latest: complete
          ? { ...version('2', 'pending'), status: 'withdrawn' }
          : version('2', 'pending'),
        capabilities: { ...row('6', revision).capabilities, can_manage: true },
      },
    ],
    next_cursor: null,
    pagination: { page: '1', page_size: 20, total_items: '1', total_pages: '1' },
  }));
  vi.stubGlobal(
    'fetch',
    vi.fn<typeof fetch>(async (input, init) => {
      expect(String(input)).toBe('/api/donations/3/uses/6/pending');
      expect(init?.method).toBe('DELETE');
      writes.push({
        body: JSON.parse(String(init?.body)),
        key: new Headers(init?.headers).get('Idempotency-Key'),
      });
      if (writes.length === 1) {
        revision = '10';
        return new Response(
          JSON.stringify({ error: { code: 'conflict', message: 'Version changed' } }),
          { status: 409, headers: { 'content-type': 'application/json' } },
        );
      }
      if (writes.length < 4) throw new TypeError('Response lost');
      complete = true;
      return new Response(null, { status: 204 });
    }),
  );
  const view = await renderWithProviders(
    <DonationUsesPanel role="owner" accountId="1" donationId="3" />,
    { station: 'user', role: 'user' },
  );
  view.queryClient.setQueryData(['user', 'session'], {
    user: { id: '1', username: 'owner', effective_level: 1 },
  });
  await view.user.click(await screen.findByText('Chat completions', { exact: true }));
  await view.user.click(screen.getByRole('button', { name: 'Withdraw pending version' }));
  let dialog = await screen.findByRole('alertdialog');
  await view.user.click(within(dialog).getByRole('button', { name: 'Confirm' }));
  await within(dialog).findByText(/Version changed/);
  await view.user.click(within(dialog).getByRole('button', { name: 'Cancel' }));
  await view.user.click(screen.getByRole('button', { name: 'Withdraw pending version' }));
  dialog = await screen.findByRole('alertdialog');
  expect(within(dialog).getByRole('button', { name: 'Confirm' })).toBeEnabled();
  await view.user.click(within(dialog).getByRole('button', { name: 'Confirm' }));
  await view.user.click(await within(dialog).findByRole('button', { name: 'Check saved result' }));
  await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull());
  expect(writes).toHaveLength(4);
  expect(writes[0]?.body).toEqual({ expected_revision: '9' });
  expect(writes[1]?.body).toEqual({ expected_revision: '10' });
  expect(writes.slice(1).every((item) => JSON.stringify(item) === JSON.stringify(writes[1]))).toBe(
    true,
  );
  expect(screen.getByText('head-1…tail · Version 1')).toBeVisible();
});

it('keeps a successful review receipt and retries only reads after a failed refresh', async () => {
  const page = (data: ReturnType<typeof row>[]) => ({
    data,
    next_cursor: null,
    pagination: { page: '1', page_size: 20, total_items: '1', total_pages: '1' },
  });
  mocks.get
    .mockResolvedValueOnce(page([row('6', '9')]))
    .mockRejectedValueOnce(new Error('Refresh temporarily unavailable'))
    .mockResolvedValue(page([row('6', '10')]));
  mocks.review.mockResolvedValue([{ use_id: '6', ok: true }]);
  const view = await renderWithProviders(
    <DonationUsesPanel role="steward" accountId="5" donationId="3" />,
    { station: 'user', role: 'level5', route: '/charity' },
  );
  view.queryClient.setQueryData(['user', 'session'], {
    user: { id: '5', username: 'reviewer', effective_level: 5 },
  });
  await view.user.click(await screen.findByText('Chat completions', { exact: true }));
  await view.user.click(screen.getByRole('checkbox', { name: 'Select use for review' }));
  await view.user.click(screen.getByRole('button', { name: 'Approve selected uses' }));
  await screen.findByText('Use 6 saved');
  const retry = await screen.findByRole('button', { name: 'Check again' });
  expect(mocks.review).toHaveBeenCalledTimes(1);
  await view.user.click(retry);
  await waitFor(() => expect(mocks.get).toHaveBeenCalledTimes(3));
  await waitFor(() =>
    expect(
      screen.queryByRole('button', { name: 'Check again' }),
    ).not.toBeInTheDocument(),
  );
  expect(screen.getByText('Use 6 saved')).toBeVisible();
  expect(mocks.review).toHaveBeenCalledTimes(1);
});
