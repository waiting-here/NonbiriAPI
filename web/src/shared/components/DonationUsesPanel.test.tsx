import { ApiError } from '@shared/query/http';
import { screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../test/unit/support';
import { useQuery } from '@tanstack/react-query';
import { charityKeys } from '@shared/operations/charity';
import { DonationUseReviewResults, DonationUsesPanel } from './DonationUsesPanel';
const mocks = vi.hoisted(() => ({ get: vi.fn(), review: vi.fn(), start: vi.fn(), read: vi.fn() }));
vi.mock('@shared/operations/donationDiscovery', async (original) => ({
  ...(await original<typeof import('@shared/operations/donationDiscovery')>()),
  startDonationDiscovery: mocks.start,
  readDonationDiscovery: mocks.read,
}));
vi.mock('@shared/operations/donationUses', async (original) => ({
  ...(await original<typeof import('@shared/operations/donationUses')>()),
  getDonationUses: mocks.get,
}));
vi.mock('@shared/operations/charity', async (original) => ({
  ...(await original<typeof import('@shared/operations/charity')>()),
  reviewPendingDonation: mocks.review,
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
const page = (data: unknown[], size = 100) => ({
  data,
  pagination: { page: '1', page_size: size, total_items: String(data.length), total_pages: '1' },
});
async function setup(extra?: React.ReactNode) {
  const view = await renderWithProviders(
    <>
      {extra}
      <DonationUsesPanel role="admin" accountId="1" donationId="3" donationRevision="1" />
    </>,
    { station: 'admin', role: 'admin' },
  );
  view.queryClient.setQueryData(['admin', 'session'], { admin: { id: '1', username: 'admin' } });
  return view;
}
describe('normal donation review details', () => {
  it('groups review receipts by key without exposing individual service decisions', async () => {
    const configure = vi.fn();
    const label = {
      use_id: '6',
      donation_key_id: '4',
      native_service: 'chat_completions' as const,
      display_head: 'head-2',
      display_tail: 'tail',
      decision: 'approve' as const,
    };
    const view = await renderWithProviders(
      <DonationUseReviewResults
        results={[
          { use_id: '6', ok: true },
          { use_id: '7', ok: true },
        ]}
        labels={[label, { ...label, use_id: '7', native_service: 'responses' }]}
        onConfigure={configure}
        configurationStatus={() => 'available'}
      />,
      { station: 'admin', role: 'admin' },
    );
    const buttons = screen.getAllByRole('button', { name: 'Configure charity models' });
    expect(buttons).toHaveLength(1);
    expect(screen.queryByText('Chat completions')).not.toBeInTheDocument();
    await view.user.click(buttons[0]!);
    expect(configure).toHaveBeenCalledWith(label);
  });
  it('shows one copy of each credential material and approves all pending content with its material revision', async () => {
    let saved = false;
    mocks.get.mockImplementation(async (_role, _donation, _page, size, state) =>
      page(
        state === 'pending' && saved
          ? []
          : ['6', '7'].map((id) => ({
              ...row(id, saved ? '10' : '9'),
              pending: saved ? null : version('2', 'pending'),
            })),
        size,
      ),
    );
    mocks.review.mockImplementation(async () => {
      saved = true;
      return {};
    });
    const view = await setup();
    await view.user.click(screen.getByText('Review material'));
    await screen.findByText('head-2…tail · Version 2');
    expect(screen.getAllByText('head-1…tail · Version 1')).toHaveLength(1);
    expect(screen.getAllByText('head-2…tail · Version 2')).toHaveLength(1);
    expect(screen.queryByRole('checkbox')).not.toBeInTheDocument();
    expect(screen.queryByText('Donation uses by service')).not.toBeInTheDocument();
    await view.user.click(screen.getByRole('button', { name: 'Approve pending content' }));
    await waitFor(() => expect(mocks.review).toHaveBeenCalledTimes(1));
    expect(mocks.review.mock.calls[0]![2]).toEqual({
      expected_revision: '1',
      decision: 'approve',
      reason: '',
      uses: ['6', '7'].map((id) => ({
        use_id: id,
        expected_revision: '9',
        expected_review_revision: 'body-2',
      })),
    });
    await waitFor(() => expect(screen.queryByText('Pending version')).not.toBeInTheDocument());
    expect(screen.queryByText(/Processed .* uses/)).not.toBeInTheDocument();
  });
  it('keeps a known atomic conflict unconfirmed and loads a fresh complete snapshot for the next action', async () => {
    let changed = false;
    mocks.get.mockImplementation(async (_role, _donation, _page, size) =>
      page([row('6', changed ? '10' : '9'), row('7', changed ? '10' : '9')], size),
    );
    mocks.review
      .mockImplementationOnce(async () => {
        changed = true;
        throw new ApiError('conflict', 'Version changed', 409);
      })
      .mockResolvedValue({});
    const view = await setup();
    await view.user.click(screen.getByRole('button', { name: 'Approve pending content' }));
    await screen.findByText('Version changed');
    expect(screen.queryByText('head-2…tail', { selector: 'li span' })).not.toBeInTheDocument();
    await view.user.click(screen.getByRole('button', { name: 'Approve pending content' }));
    await waitFor(() => expect(mocks.review).toHaveBeenCalledTimes(2));
    expect(mocks.review.mock.calls[1]![2].uses).toEqual(
      ['6', '7'].map((id) => ({
        use_id: id,
        expected_revision: '10',
        expected_review_revision: 'body-2',
      })),
    );
    expect(mocks.review.mock.calls[1]![3]).not.toBe(mocks.review.mock.calls[0]![3]);
  });
  it('recovers an uncertain whole-content write using the original body and key', async () => {
    mocks.get.mockImplementation(async (_role, _donation, _page, size) =>
      page([row('6', '9'), row('7', '9')], size),
    );
    let attempts = 0;
    mocks.review.mockImplementation(async () => {
      if (++attempts < 3) throw new ApiError('network_error', 'Response lost', 0);
      return {};
    });
    const view = await setup();
    await view.user.click(screen.getByRole('button', { name: 'Approve pending content' }));
    await view.user.click(await screen.findByRole('button', { name: 'Check saved result' }));
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Approve pending content' })).toBeEnabled(),
    );
    expect(mocks.review).toHaveBeenCalledTimes(3);
    expect(mocks.review.mock.calls[0]!.slice(2, 4)).toEqual(
      mocks.review.mock.calls[2]!.slice(2, 4),
    );
  });
  it('retains successful write receipts and retries only reads after a real refresh error', async () => {
    let saved = false,
      failed = false;
    mocks.get.mockImplementation(async (_role, _donation, _page, size) => {
      if (saved && !failed) {
        failed = true;
        throw new Error('Refresh unavailable');
      }
      return page([row('6', saved ? '10' : '9')], size);
    });
    mocks.review.mockImplementation(async () => {
      saved = true;
      return [{ use_id: '6', ok: true }];
    });
    const view = await setup();
    await view.user.click(screen.getByRole('button', { name: 'Approve pending content' }));
    const retry = await screen.findByRole('button', { name: 'Check again' });
    await view.user.click(retry);
    await waitFor(() =>
      expect(screen.queryByRole('button', { name: 'Check again' })).not.toBeInTheDocument(),
    );
    expect(mocks.review).toHaveBeenCalledTimes(1);
  });
  it('refreshes current donation data without invalidating active old-revision discovery', async () => {
    let saved = false;
    mocks.get.mockImplementation(async (_role, _donation, _page, size) =>
      page(
        [{ ...row('6', saved ? '10' : '9'), pending: saved ? null : version('2', 'pending') }],
        size,
      ),
    );
    mocks.review.mockImplementation(async () => {
      saved = true;
      return [{ use_id: '6', ok: true }];
    });
    const discovery = vi.fn(async () => {
      if (saved) throw new ApiError('conflict', 'Old discovery version', 409);
      return 'catalog';
    });
    const list = vi.fn(async () => (saved ? 'approved' : 'pending'));
    function ActiveQueries() {
      const old = useQuery({
        queryKey: ['admin', 'operations', 'use-discovery', '1', '6', '9'],
        queryFn: discovery,
      });
      const current = useQuery({
        queryKey: [...charityKeys.root('admin'), 'donation-pages', '1'],
        queryFn: list,
      });
      return (
        <p>
          {old.data} / Donation list: {current.data}
        </p>
      );
    }
    const view = await setup(<ActiveQueries />);
    await screen.findByText('catalog / Donation list: pending');
    await view.user.click(screen.getByRole('button', { name: 'Approve pending content' }));
    await screen.findByText('catalog / Donation list: approved');
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Approve pending content' })).toBeEnabled(),
    );
    expect(discovery).toHaveBeenCalledTimes(1);
    expect(list).toHaveBeenCalledTimes(2);
    expect(screen.queryByRole('button', { name: 'Check again' })).not.toBeInTheDocument();
  });
  it('returns replacement content while preserving the old approved material', async () => {
    let saved = false;
    mocks.get.mockImplementation(async (_role, _donation, _page, size) =>
      page(
        [{ ...row('6', saved ? '10' : '9'), pending: saved ? null : version('2', 'pending') }],
        size,
      ),
    );
    mocks.review.mockImplementation(async () => {
      saved = true;
      return [{ use_id: '6', ok: true }];
    });
    const view = await setup();
    await view.user.click(screen.getByText('Review material'));
    await screen.findByText('Pending version');
    await view.user.click(screen.getByRole('button', { name: 'Return pending content' }));
    await waitFor(() => expect(screen.queryByText('Pending version')).not.toBeInTheDocument());
    expect(screen.getByText('head-1…tail · Version 1')).toBeVisible();
    expect(mocks.review.mock.calls[0]![2]).toMatchObject({
      decision: 'reject',
      uses: [{ use_id: '6', expected_review_revision: 'body-2' }],
    });
  });
  it('does not write when no content is pending', async () => {
    mocks.get.mockImplementation(async (_role, _donation, _page, size) => page([], size));
    const view = await setup();
    await view.user.click(screen.getByRole('button', { name: 'Approve pending content' }));
    await screen.findByText('There is no pending content to review.');
    expect(mocks.review).not.toHaveBeenCalled();
  });
});
