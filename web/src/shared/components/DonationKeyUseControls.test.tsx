import { screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { ApiError } from '@shared/query/http';
import { renderWithProviders } from '../../../test/unit/support';
import type { DonationUse } from '@shared/operations/donationUses';
import { DonationKeyUseControls } from './DonationKeyUseControls';

const mocks = vi.hoisted(() => ({ get: vi.fn(), mutate: vi.fn() }));
vi.mock('@shared/operations/donationUses', async (original) => ({
  ...(await original<typeof import('@shared/operations/donationUses')>()),
  getDonationUses: mocks.get,
  mutateDonationUse: mocks.mutate,
}));
const version = {
  credential_id: '8',
  credential_version: '1',
  grant_revision: '1',
  status: 'approved',
  review_note: '',
  reviewed_at: null,
  expected_review_revision: null,
  discovery_source_id: '51',
  discovery_source_revision: '1',
  display_head: 'head',
  display_tail: 'tail',
  review: { required: false, revision: null, material_available: true },
  discovery: null,
} as const;
const row = (id: string, enabled = true): DonationUse => ({
  id,
  donation_id: '3',
  donation_key_id: '4',
  endpoint_key_id: '5',
  native_service: id === '6' ? 'chat_completions' : 'responses',
  enabled,
  discovery_enabled: false,
  revision: '9',
  approved: version,
  pending: { ...version, status: 'pending' },
  latest: version,
  health: null,
  capabilities: {
    can_review: false,
    can_manage: true,
    can_reset_failures: false,
    can_discover: false,
    can_bind: false,
    unavailable_reason: null,
  },
});
async function setup(rows: DonationUse[]) {
  mocks.get.mockImplementation(async () => ({
    data: rows,
    pagination: { page: '1', page_size: 100, total_items: String(rows.length), total_pages: '1' },
  }));
  mocks.mutate.mockReset();
  const view = await renderWithProviders(
    <DonationKeyUseControls role="owner" accountId="1" donationId="3" donationKeyId="4" />,
    { station: 'user', role: 'user' },
  );
  view.queryClient.setQueryData(['user', 'session'], {
    user: { id: '1', username: 'owner', effective_level: 1 },
  });
  await screen.findByRole('button', { name: 'Pause sharing' });
  return view;
}
describe('key-wide sharing controls', () => {
  it('pauses only active services and resumes a partially completed action without repeating confirmed writes', async () => {
    const rows = [
      row('6'),
      row('7'),
      { ...row('8', false), native_service: 'embeddings' as const },
    ];
    const view = await setup(rows);
    let lost = true;
    mocks.mutate.mockImplementation(async (_role, _donation, id) => {
      if (id === '7' && lost) {
        lost = false;
        throw new ApiError('network_error', 'Response lost', 0);
      }
      rows.find((entry) => entry.id === id)!.enabled = false;
    });
    await view.user.click(screen.getByRole('button', { name: 'Pause sharing' }));
    await screen.findByText('Saved.');
    expect(mocks.mutate.mock.calls.map((call) => call[2])).toEqual(['6', '7', '7']);
    expect(mocks.mutate.mock.calls[0]![4]).toEqual({ expected_revision: '9', enabled: false });
    expect(mocks.mutate.mock.calls[1]!.slice(2, 6)).toEqual(
      mocks.mutate.mock.calls[2]!.slice(2, 6),
    );
    expect(mocks.mutate.mock.calls.some((call) => call[2] === '8')).toBe(false);
    expect(screen.queryByRole('checkbox')).not.toBeInTheDocument();
  });
  it('keeps uncertain withdrawal recovery inside the single key confirmation', async () => {
    const view = await setup([row('6')]);
    let attempts = 0;
    mocks.mutate.mockImplementation(async () => {
      if (++attempts < 3) throw new ApiError('network_error', 'Response lost', 0);
    });
    await view.user.click(screen.getByRole('button', { name: 'Withdraw pending content' }));
    const dialog = await screen.findByRole('alertdialog');
    await view.user.click(within(dialog).getByRole('button', { name: 'Withdraw pending content' }));
    await view.user.click(
      await within(dialog).findByRole('button', { name: 'Check saved result' }),
    );
    await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument());
    expect(mocks.mutate).toHaveBeenCalledTimes(3);
    expect(mocks.mutate.mock.calls[0]!.slice(2, 6)).toEqual(
      mocks.mutate.mock.calls[2]!.slice(2, 6),
    );
  });
});
