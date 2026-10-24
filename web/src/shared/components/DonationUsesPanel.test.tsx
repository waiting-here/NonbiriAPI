import { ApiError } from '@shared/query/http';
import { screen, waitFor, within } from '@testing-library/react';
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
describe('pending content review', () => {
  it('continues from the approved service and preserves its credential label after review', async () => {
    const configure = vi.fn();
    const approved = {
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
          { use_id: '7', ok: false, error: { code: 'conflict', message: 'Version changed' } },
          { use_id: '8', ok: true },
        ]}
        labels={[
          approved,
          { ...approved, use_id: '7', native_service: 'responses' },
          { ...approved, use_id: '8', decision: 'reject' },
        ]}
        onConfigure={configure}
        configurationStatus={() => 'available'}
      />,
      { station: 'admin', role: 'admin' },
    );
    const buttons = screen.getAllByRole('button', { name: 'Configure charity models' });
    expect(buttons).toHaveLength(1);
    expect(buttons[0]!.closest('li')).toHaveTextContent('Chat completions');
    expect(buttons[0]!.closest('li')).toHaveTextContent('head-2…tail');
    await view.user.click(buttons[0]!);
    expect(configure).toHaveBeenCalledWith(approved);
  });

  it('reviews all pending content without selection and loads the current failed content on a new action', async () => {
    let revision = '9';
    let approved = false;
    mocks.get.mockImplementation(async (_role, _donation, _page, size, state) => ({
      data:
        state === 'pending' && approved
          ? [row('7', revision)]
          : [
              { ...row('6', '9'), pending: approved ? null : version('2', 'pending') },
              row('7', revision),
            ],
      next_cursor: null,
      pagination: {
        page: '1',
        page_size: size,
        total_items: state === 'pending' && approved ? '1' : '2',
        total_pages: '1',
      },
    }));
    mocks.review
      .mockImplementationOnce(async () => {
        revision = '10';
        approved = true;
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
    expect(screen.queryByRole('checkbox')).not.toBeInTheDocument();
    expect(screen.getAllByText('head-1…tail · Version 1')).toHaveLength(2);
    expect(screen.getAllByText('head-2…tail · Version 2')).toHaveLength(2);
    expect(screen.getAllByRole('button', { name: 'Fetch model list' })).toHaveLength(2);
    await view.user.click(screen.getByRole('button', { name: 'Approve pending content' }));
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument();
    await waitFor(() => expect(mocks.review).toHaveBeenCalledTimes(1));
    await screen.findByText(/Version changed/);
    await screen.findByText('Processed 2 of 2 uses: 1 succeeded, 1 failed.');
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Approve pending content' })).toBeEnabled(),
    );
    await view.user.click(screen.getByRole('button', { name: 'Approve pending content' }));
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
  let saved = false;
  let failed = false;
  mocks.get.mockImplementation(async (_role, _donation, _page, size) => {
    if (saved && !failed) {
      failed = true;
      throw new Error('Refresh temporarily unavailable');
    }
    return {
      data: [row('6', saved ? '10' : '9')],
      pagination: { page: '1', page_size: size, total_items: '1', total_pages: '1' },
    };
  });
  mocks.review.mockImplementation(async () => {
    saved = true;
    return [{ use_id: '6', ok: true }];
  });
  const view = await renderWithProviders(
    <DonationUsesPanel role="steward" accountId="5" donationId="3" />,
    { station: 'user', role: 'level5', route: '/charity' },
  );
  view.queryClient.setQueryData(['user', 'session'], {
    user: { id: '5', username: 'reviewer', effective_level: 5 },
  });
  await view.user.click(await screen.findByText('Chat completions', { exact: true }));
  await view.user.click(screen.getByRole('button', { name: 'Approve pending content' }));
  await screen.findByText('Use 6 saved');
  const retry = await screen.findByRole('button', { name: 'Check again' });
  expect(mocks.review).toHaveBeenCalledTimes(1);
  await view.user.click(retry);
  await waitFor(() => expect(mocks.get).toHaveBeenCalledTimes(4));
  await waitFor(() =>
    expect(screen.queryByRole('button', { name: 'Check again' })).not.toBeInTheDocument(),
  );
  expect(screen.getByText('Use 6 saved')).toBeVisible();
  expect(mocks.review).toHaveBeenCalledTimes(1);
});

it('returns pending content across all key pages in one action and reports unavailable material as unprocessed', async () => {
  const pending = Array.from({ length: 102 }, (_, index) => ({
    ...row(String(index + 1), '9'),
    pending: {
      ...version('2', 'pending'),
      review: { required: true, revision: 'body-2', material_available: index !== 101 },
    },
    capabilities: { ...row('6', '9').capabilities, can_review: index !== 101 },
  }));
  mocks.get.mockImplementation(async (_role, _donation, pageNumber, size, state, _signal, key) => {
    expect(key).toBe('4');
    return {
      data:
        state === 'pending'
          ? pending.slice((Number(pageNumber) - 1) * 100, Number(pageNumber) * 100)
          : pending.slice(0, 1),
      pagination: {
        page: String(pageNumber),
        page_size: size,
        total_items: state === 'pending' ? '102' : '1',
        total_pages: state === 'pending' ? '2' : '1',
      },
    };
  });
  mocks.review.mockImplementation(async (_role, _donation, items) => {
    pending[0]!.revision = '10';
    return items.map((item: { use_id: string }) => ({ use_id: item.use_id, ok: true }));
  });
  const view = await renderWithProviders(
    <DonationUsesPanel role="steward" accountId="5" donationId="3" donationKeyId="4" />,
    { station: 'user', role: 'level5', route: '/charity' },
  );
  view.queryClient.setQueryData(['user', 'session'], {
    user: { id: '5', username: 'reviewer', effective_level: 5 },
  });
  await view.user.type(screen.getByLabelText('Review reason'), 'Please update the material');
  await view.user.click(await screen.findByRole('button', { name: 'Return pending content' }));
  expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument();
  await waitFor(() => expect(mocks.review).toHaveBeenCalledTimes(2));
  expect(mocks.review.mock.calls.map((call) => call[2].length)).toEqual([100, 1]);
  expect(mocks.review.mock.calls[0]![2][0]).toMatchObject({
    use_id: '1',
    expected_revision: '9',
    decision: 'reject',
    expected_review_revision: 'body-2',
    review_note: 'Please update the material',
  });
  expect(mocks.review.mock.calls[1]![2][0].use_id).toBe('101');
  await screen.findByText('Processed 101 of 102 uses: 101 succeeded, 0 failed.');
  await view.user.click(screen.getByText('View blocked uses'));
  expect(screen.getByText(/Review material unavailable/)).toBeVisible();
  expect(screen.getByText('head-1…tail · Version 1')).not.toBeVisible();
});

it('does not write when there is no pending content', async () => {
  mocks.get.mockImplementation(async (_role, _donation, _page, size) => ({
    data: [],
    pagination: { page: '1', page_size: size, total_items: '0', total_pages: '1' },
  }));
  const view = await renderWithProviders(
    <DonationUsesPanel role="admin" accountId="1" donationId="3" />,
    { station: 'admin' },
  );
  view.queryClient.setQueryData(['admin', 'session'], { admin: { id: '1', username: 'admin' } });
  await view.user.click(await screen.findByRole('button', { name: 'Approve pending content' }));
  await screen.findByText('There is no pending content to review.');
  expect(mocks.review).not.toHaveBeenCalled();
  expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument();
});

it('refreshes donation data without refetching an active discovery query for an old use revision', async () => {
  let saved = false;
  const evidence = {
    state: 'succeeded',
    revision: '1',
    result: 'populated',
    safe_class: 'none',
    observed_at: 1,
    count: '3',
  };
  const currentEvidence = { ...evidence, count: '4' };
  mocks.get.mockImplementation(async (_role, _donation, _page, size) => ({
    data: [
      {
        ...row('6', saved ? '10' : '9'),
        approved: saved
          ? { ...version('2', 'approved'), discovery: currentEvidence }
          : version('1', 'approved'),
        pending: saved ? null : version('2', 'pending'),
        capabilities: { ...row('6', '9').capabilities, can_discover: true },
      },
    ],
    pagination: { page: '1', page_size: size, total_items: '1', total_pages: '1' },
  }));
  mocks.start.mockResolvedValue(evidence);
  mocks.read.mockImplementation(async (_role, input) => {
    if (saved && input.expected_revision === '9')
      throw new ApiError('conflict', 'Old discovery version', 409);
    return evidence;
  });
  mocks.review.mockImplementation(async () => {
    saved = true;
    return [{ use_id: '6', ok: true }];
  });
  const readList = vi.fn(async () => (saved ? 'approved' : 'pending'));
  function DonationList() {
    const query = useQuery({
      queryKey: [...charityKeys.root('admin'), 'donation-pages', '1'],
      queryFn: readList,
    });
    return <p>Donation list: {query.data}</p>;
  }
  const view = await renderWithProviders(
    <>
      <DonationList />
      <DonationUsesPanel role="admin" accountId="1" donationId="3" />
    </>,
    { station: 'admin', role: 'admin' },
  );
  view.queryClient.setQueryData(['admin', 'session'], { admin: { id: '1', username: 'admin' } });
  await view.user.click(await screen.findByText('Chat completions', { exact: true }));
  await view.user.click(screen.getByRole('button', { name: 'Fetch model list' }));
  await screen.findByText('Catalog: Succeeded · Models 3');
  await waitFor(() => expect(mocks.read).toHaveBeenCalledTimes(1));
  await view.user.click(screen.getByRole('button', { name: 'Approve pending content' }));
  await screen.findByText('Donation list: approved');
  await screen.findByText('Catalog: Succeeded · Models 4');
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Approve pending content' })).toBeEnabled(),
  );
  expect(readList).toHaveBeenCalledTimes(2);
  expect(mocks.read).toHaveBeenCalledTimes(1);
  expect(screen.queryByText('Old discovery version')).not.toBeInTheDocument();
  expect(screen.queryByRole('button', { name: 'Check again' })).not.toBeInTheDocument();
  expect(screen.getByText('Use 6 saved')).toBeVisible();
});

it('returns replacement content while retaining the previously approved version', async () => {
  let saved = false;
  mocks.get.mockImplementation(async (_role, _donation, _page, size) => ({
    data: [
      {
        ...row('6', saved ? '10' : '9'),
        pending: saved ? null : version('2', 'pending'),
        latest: saved
          ? { ...version('2', 'pending'), status: 'rejected' }
          : version('2', 'pending'),
      },
    ],
    pagination: { page: '1', page_size: size, total_items: '1', total_pages: '1' },
  }));
  mocks.review.mockImplementation(async () => {
    saved = true;
    return [{ use_id: '6', ok: true }];
  });
  const view = await renderWithProviders(
    <DonationUsesPanel role="admin" accountId="1" donationId="3" />,
    { station: 'admin', role: 'admin' },
  );
  view.queryClient.setQueryData(['admin', 'session'], { admin: { id: '1', username: 'admin' } });
  await view.user.click(await screen.findByText('Chat completions', { exact: true }));
  await view.user.click(screen.getByRole('button', { name: 'Return pending content' }));
  await waitFor(() => expect(screen.queryByText('Pending version')).not.toBeInTheDocument());
  expect(screen.getByText('Approved version')).toBeVisible();
  expect(screen.getByText('head-1…tail · Version 1')).toBeVisible();
  expect(mocks.review.mock.calls[0]![2]).toEqual([
    {
      use_id: '6',
      expected_revision: '9',
      expected_review_revision: 'body-2',
      decision: 'reject',
      review_note: '',
    },
  ]);
});

it.each([403, 0])(
  'only lets a known batch failure finish the operation (status %s)',
  async (status) => {
    const rows = Array.from({ length: 101 }, (_, index) => row(String(index + 1), '9'));
    mocks.get.mockImplementation(async (_role, _donation, number, size, state) => ({
      data:
        state === 'pending'
          ? rows.slice((Number(number) - 1) * 100, Number(number) * 100)
          : rows.slice(0, 1),
      pagination: {
        page: String(number),
        page_size: size,
        total_items: state === 'pending' ? '101' : '1',
        total_pages: state === 'pending' ? '2' : '1',
      },
    }));
    mocks.review
      .mockImplementationOnce(async (_role, _donation, items) =>
        items.map((item: { use_id: string }) => ({ use_id: item.use_id, ok: true })),
      )
      .mockRejectedValue(
        new ApiError(status === 403 ? 'forbidden' : 'result_unknown', 'Review interrupted', status),
      );
    const view = await renderWithProviders(
      <DonationUsesPanel role="admin" accountId="1" donationId="3" />,
      { station: 'admin' },
    );
    view.queryClient.setQueryData(['admin', 'session'], { admin: { id: '1', username: 'admin' } });
    await view.user.click(await screen.findByRole('button', { name: 'Approve pending content' }));
    await screen.findByText('Processed 100 of 101 uses: 100 succeeded, 0 failed.');
    if (status === 403) {
      await view.user.click(
        await screen.findByRole('button', {
          name: 'Finish review',
        }),
      );
      expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument();
      expect(screen.getByRole('button', { name: 'Approve pending content' })).toBeEnabled();
    } else {
      await waitFor(() => expect(mocks.review).toHaveBeenCalledTimes(3));
      expect(
        screen.queryByRole('button', {
          name: 'Finish review',
        }),
      ).not.toBeInTheDocument();
      expect(screen.getByRole('button', { name: 'Check saved result' })).toBeEnabled();
      expect(mocks.review.mock.calls[2]![3]).toBe(mocks.review.mock.calls[1]![3]);
      mocks.review.mockResolvedValue([{ use_id: '101', ok: true }]);
      await view.user.click(screen.getByRole('button', { name: 'Check saved result' }));
      await screen.findByText('Processed 101 of 101 uses: 101 succeeded, 0 failed.');
      expect(mocks.review.mock.calls[3]![2]).toEqual(mocks.review.mock.calls[1]![2]);
      expect(mocks.review.mock.calls[3]![3]).toBe(mocks.review.mock.calls[1]![3]);
      expect(screen.getByRole('button', { name: 'Approve pending content' })).toBeEnabled();
    }
  },
);

it.each(['pending', 'approved'] as const)(
  'fetches an unshared %s donation catalog directly',
  async (status) => {
    const use = row('6', '9');
    const previous = {
      state: 'unknown',
      revision: '1',
      result: null,
      safe_class: 'none',
      observed_at: null,
      count: null,
    };
    mocks.get.mockResolvedValue({
      data: [
        {
          ...use,
          approved: status === 'approved' ? { ...use.approved, discovery: previous } : null,
          pending: status === 'pending' ? { ...use.pending, discovery: previous } : null,
          capabilities: { ...use.capabilities, can_discover: true },
        },
      ],
      pagination: { page: '1', page_size: 20, total_items: '1', total_pages: '1' },
    });
    const evidence = {
      state: 'succeeded',
      revision: '1',
      result: 'populated',
      safe_class: 'none',
      observed_at: 1,
      count: '3',
    };
    mocks.start.mockResolvedValue(evidence);
    mocks.read.mockResolvedValue(evidence);
    const view = await renderWithProviders(
      <DonationUsesPanel role="admin" accountId="1" donationId="3" />,
      { station: 'admin', role: 'admin' },
    );
    view.queryClient.setQueryData(['admin', 'session'], { admin: { username: 'admin' } });
    await view.user.click(await screen.findByText('Chat completions', { exact: true }));
    const button = screen.getByRole('button', { name: 'Fetch model list' });
    expect(button).toBeEnabled();
    await view.user.click(button);
    await waitFor(() =>
      expect(mocks.start).toHaveBeenCalledWith(
        'admin',
        {
          donation_id: '3',
          key_id: '4',
          use_id: '6',
          expected_revision: '9',
        },
        expect.any(String),
      ),
    );
    await waitFor(() => expect(mocks.read).toHaveBeenCalled());
    await screen.findByText('Catalog: Succeeded · Models 3');
    expect(screen.getAllByText(/^Catalog:/)).toHaveLength(1);
  },
);
