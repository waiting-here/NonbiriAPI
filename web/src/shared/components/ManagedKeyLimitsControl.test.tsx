import { fireEvent, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { charityKeys, type ManagedDonationKey } from '@shared/operations/charity';
import { ApiError } from '@shared/query/http';
import { renderWithProviders } from '../../../test/unit/support';
import { ManagedKeyLimitsControl } from './ManagedKeyLimitsControl';

const { patch } = vi.hoisted(() => ({ patch: vi.fn() }));
vi.mock('@shared/operations/charity', async (original) => ({
  ...(await original<typeof import('@shared/operations/charity')>()),
  patchManagedDonationKey: patch,
}));

const item: ManagedDonationKey = {
  id: '11',
  endpoint_key_id: '21',
  key_limits_revision: '5',
  rate_limited_until: null,
  failure_disable_threshold: '10',
  smart_backoff_profile: 'none' as const,
  smart_backoff_rules: [],
  smart_backoff: null,
  binding_count: '0',
  idle: true,
  display_head: 'head',
  display_tail: 'tail',
  safe_source: {
    kind: 'custom',
    base_url: 'https://example.test/v1',
    connector_type: 'openai-compatible',
  },
  physical_enabled: true,
  charity_state: 'available',
  limits: { price: null, calls: null, tokens: null },
  usage: {
    price_used: '0',
    price_inflight: '0',
    calls_used: '0',
    calls_inflight: '0',
    tokens_used: '0',
    tokens_inflight: '0',
  },
  token_reserve: 32,
  authorized_expires_at: null,
  expires_at: null,
  streak: { generation: '1', count: '0', failure_disabled: false },
  ended_reason: null,
  safe_note: '',
  max_concurrency: 2,
  max_rpm: 30,
};
const donation = { id: '1', revision: '7', status: 'approved' };
type Authority = { item: ManagedDonationKey; donation: typeof donation };
const authorityKey = [...charityKeys.root('admin'), 'limits-authority'] as const;

function QueryBackedLimits({ load }: { load: () => Promise<Authority> }) {
  const client = useQueryClient();
  const query = useQuery({
    queryKey: authorityKey,
    queryFn: load,
    initialData: { item, donation },
    staleTime: Infinity,
    retry: false,
  });
  return (
    <ManagedKeyLimitsControl
      {...query.data}
      role="admin"
      refresh={async () => {
        await client.invalidateQueries({ queryKey: authorityKey }, { throwOnError: true });
        const current = client.getQueryData<Authority>(authorityKey)!;
        return { item: current.item, donationRevision: current.donation.revision };
      }}
    />
  );
}

async function setup(
  refresh = vi.fn().mockResolvedValue({ item, donationRevision: donation.revision }),
  value = item,
) {
  const props = { item: value, donation, role: 'admin' as const, refresh };
  const result = await renderWithProviders(<ManagedKeyLimitsControl {...props} />, {
    station: 'admin',
  });
  result.queryClient.setQueryData(['admin', 'session'], { admin: { username: 'fixture-admin' } });
  return { ...result, refresh, props };
}

describe('managed shared key limits', () => {
  it('uses revisions from an asynchronous query reread without a manual rerender', async () => {
    patch.mockReset().mockResolvedValue(undefined);
    const load = vi.fn(async () => {
      const response = new Response(
        JSON.stringify({
          item: { ...item, key_limits_revision: '6', max_rpm: 40 },
          donation: { ...donation, revision: '8' },
        }),
        { headers: { 'Content-Type': 'application/json' } },
      );
      return (await response.json()) as Authority;
    });
    const view = await renderWithProviders(<QueryBackedLimits load={load} />, { station: 'admin' });
    view.queryClient.setQueryData(['admin', 'session'], { admin: { username: 'fixture-admin' } });
    fireEvent.change(screen.getByRole('spinbutton', { name: 'Maximum RPM' }), {
      target: { value: '90' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Refresh and keep input' }));
    await waitFor(() => expect(load).toHaveBeenCalledTimes(1));
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Save shared limits' })).toBeEnabled(),
    );
    expect(screen.getByRole('spinbutton', { name: 'Maximum RPM' })).toHaveValue(90);
    fireEvent.click(screen.getByRole('button', { name: 'Save shared limits' }));
    await waitFor(() => expect(patch).toHaveBeenCalledTimes(1));
    expect(patch.mock.calls[0]?.[3]).toEqual({
      expected_revision: '8',
      expected_key_limits_revision: '6',
      max_rpm: 90,
    });
  });

  it('uses the successful asynchronous reread as the clean baseline for a later save', async () => {
    patch.mockReset().mockResolvedValue(undefined);
    const load = vi.fn<() => Promise<Authority>>().mockResolvedValue({
      item: { ...item, key_limits_revision: '6', max_rpm: 60 },
      donation: { ...donation, revision: '8' },
    });
    const view = await renderWithProviders(<QueryBackedLimits load={load} />, { station: 'admin' });
    view.queryClient.setQueryData(['admin', 'session'], { admin: { username: 'fixture-admin' } });
    fireEvent.change(screen.getByRole('spinbutton', { name: 'Maximum RPM' }), {
      target: { value: '60' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Save shared limits' }));
    await screen.findByText('Shared limits saved.');
    await waitFor(() => expect(load).toHaveBeenCalledTimes(1));
    await waitFor(() =>
      expect(screen.getByRole('spinbutton', { name: 'Maximum RPM' })).toBeEnabled(),
    );
    expect(screen.getByRole('spinbutton', { name: 'Maximum RPM' })).toHaveValue(60);
    expect(screen.getByRole('button', { name: 'Save shared limits' })).toBeDisabled();
    fireEvent.change(screen.getByRole('spinbutton', { name: 'Maximum RPM' }), {
      target: { value: '90' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Save shared limits' }));
    await waitFor(() => expect(patch).toHaveBeenCalledTimes(2));
    expect(patch.mock.calls[1]?.[3]).toEqual({
      expected_revision: '8',
      expected_key_limits_revision: '6',
      max_rpm: 90,
    });
  });

  it('keeps a failed asynchronous reread read only, then retries only the read and adopts its revisions', async () => {
    patch.mockReset().mockResolvedValue(undefined);
    const load = vi
      .fn<() => Promise<Authority>>()
      .mockRejectedValueOnce(new Error('Read unavailable'))
      .mockResolvedValue({
        item: { ...item, key_limits_revision: '6', max_rpm: 60 },
        donation: { ...donation, revision: '8' },
      });
    const view = await renderWithProviders(<QueryBackedLimits load={load} />, { station: 'admin' });
    view.queryClient.setQueryData(['admin', 'session'], { admin: { username: 'fixture-admin' } });
    fireEvent.change(screen.getByRole('spinbutton', { name: 'Maximum RPM' }), {
      target: { value: '60' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Save shared limits' }));
    const retry = await screen.findByRole('button', { name: 'Retry' });
    expect(screen.getByRole('spinbutton', { name: 'Maximum RPM' })).toBeDisabled();
    fireEvent.click(retry);
    await waitFor(() =>
      expect(screen.getByRole('spinbutton', { name: 'Maximum RPM' })).toBeEnabled(),
    );
    expect(load).toHaveBeenCalledTimes(2);
    expect(patch).toHaveBeenCalledTimes(1);
    expect(screen.getByRole('spinbutton', { name: 'Maximum RPM' })).toHaveValue(60);
    expect(screen.getByRole('button', { name: 'Save shared limits' })).toBeDisabled();
    fireEvent.change(screen.getByRole('spinbutton', { name: 'Maximum RPM' }), {
      target: { value: '90' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Save shared limits' }));
    await waitFor(() => expect(patch).toHaveBeenCalledTimes(2));
    expect(patch.mock.calls[1]?.[3]).toEqual({
      expected_revision: '8',
      expected_key_limits_revision: '6',
      max_rpm: 90,
    });
  });

  it('follows authoritative updates while the form is clean without creating a pending edit', async () => {
    patch.mockReset();
    const { props, rerender } = await setup();
    rerender(
      <ManagedKeyLimitsControl
        {...props}
        donation={{ ...donation, revision: '8' }}
        item={{ ...item, key_limits_revision: '6', max_concurrency: 3, max_rpm: 40 }}
      />,
    );
    expect(screen.getByRole('spinbutton', { name: 'Maximum concurrency' })).toHaveValue(3);
    expect(screen.getByRole('spinbutton', { name: 'Maximum RPM' })).toHaveValue(40);
    expect(screen.getByRole('button', { name: 'Save shared limits' })).toBeDisabled();
    expect(patch).not.toHaveBeenCalled();
  });

  it('keeps both original revisions and unchanged fields out of a dirty save after a background update', async () => {
    patch.mockReset().mockResolvedValue(undefined);
    const { props, rerender } = await setup();
    fireEvent.change(screen.getByRole('spinbutton', { name: 'Maximum RPM' }), {
      target: { value: '90' },
    });
    rerender(
      <ManagedKeyLimitsControl
        {...props}
        donation={{ ...donation, revision: '8' }}
        item={{ ...item, key_limits_revision: '6', max_concurrency: 3, max_rpm: 40 }}
      />,
    );
    expect(screen.getByRole('spinbutton', { name: 'Maximum concurrency' })).toHaveValue(2);
    expect(screen.getByRole('spinbutton', { name: 'Maximum RPM' })).toHaveValue(90);
    fireEvent.click(screen.getByRole('button', { name: 'Save shared limits' }));
    await waitFor(() => expect(patch).toHaveBeenCalledTimes(1));
    expect(patch.mock.calls[0]?.[3]).toEqual({
      expected_revision: '7',
      expected_key_limits_revision: '5',
      max_rpm: 90,
    });
  });

  it('submits only changed integer limits with both current revisions', async () => {
    patch.mockReset().mockResolvedValue(undefined);
    await setup();
    fireEvent.change(screen.getByRole('spinbutton', { name: 'Maximum concurrency' }), {
      target: { value: '4' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Save shared limits' }));
    await waitFor(() => expect(patch).toHaveBeenCalledTimes(1));
    expect(patch.mock.calls[0]?.slice(0, 4)).toEqual([
      'admin',
      '1',
      '11',
      { expected_revision: '7', expected_key_limits_revision: '5', max_concurrency: 4 },
    ]);
  });

  it('preserves a conflict draft across new revisions and requires explicit refresh before saving', async () => {
    patch
      .mockReset()
      .mockRejectedValueOnce(new ApiError('revision_conflict', 'Changed', 409))
      .mockResolvedValue(undefined);
    const { props, rerender, refresh } = await setup();
    fireEvent.change(screen.getByRole('spinbutton', { name: 'Maximum RPM' }), {
      target: { value: '90' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Save shared limits' }));
    await screen.findByText(
      'The settings changed. Your input is preserved. Refresh, review, and save again.',
    );
    rerender(
      <ManagedKeyLimitsControl
        {...props}
        donation={{ ...donation, revision: '8' }}
        item={{ ...item, key_limits_revision: '6', max_rpm: 40 }}
      />,
    );
    expect(screen.getByRole('spinbutton', { name: 'Maximum RPM' })).toHaveValue(90);
    expect(screen.getByRole('button', { name: 'Save shared limits' })).toBeDisabled();
    refresh.mockResolvedValue({
      item: { ...item, key_limits_revision: '6', max_rpm: 40 },
      donationRevision: '8',
    });
    fireEvent.click(screen.getByRole('button', { name: 'Refresh and keep input' }));
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Save shared limits' })).toBeEnabled(),
    );
    fireEvent.click(screen.getByRole('button', { name: 'Save shared limits' }));
    await waitFor(() => expect(patch).toHaveBeenCalledTimes(2));
    expect(patch.mock.calls[1]?.[3]).toEqual({
      expected_revision: '8',
      expected_key_limits_revision: '6',
      max_rpm: 90,
    });
  });

  it('retries an unknown result with the identical body and idempotency key', async () => {
    patch
      .mockReset()
      .mockRejectedValueOnce(new ApiError('network_error', 'Failed to fetch', 0))
      .mockResolvedValue(undefined);
    await setup();
    fireEvent.change(screen.getByRole('spinbutton', { name: 'Maximum concurrency' }), {
      target: { value: '0' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Save shared limits' }));
    const retry = await screen.findByRole('button', { name: 'Retry the original action' });
    expect(screen.getByRole('spinbutton', { name: 'Maximum concurrency' })).toBeDisabled();
    fireEvent.click(retry);
    await waitFor(() => expect(patch).toHaveBeenCalledTimes(2));
    expect(patch.mock.calls[1]).toEqual(patch.mock.calls[0]);
  });

  it('retries only the read after a confirmed save cannot refresh', async () => {
    patch.mockReset().mockResolvedValue(undefined);
    const refresh = vi
      .fn()
      .mockRejectedValueOnce(new Error('Read unavailable'))
      .mockResolvedValue({ item, donationRevision: donation.revision });
    await setup(refresh);
    fireEvent.change(screen.getByRole('spinbutton', { name: 'Maximum RPM' }), {
      target: { value: '60' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Save shared limits' }));
    await screen.findByRole('button', { name: 'Retry' });
    expect(screen.getByRole('spinbutton', { name: 'Maximum RPM' })).toBeDisabled();
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    await waitFor(() => expect(refresh).toHaveBeenCalledTimes(2));
    expect(patch).toHaveBeenCalledTimes(1);
  });

  it('becomes clean after a successful refresh and follows later authoritative settings', async () => {
    patch.mockReset().mockResolvedValue(undefined);
    const { props, rerender } = await setup();
    fireEvent.change(screen.getByRole('spinbutton', { name: 'Maximum RPM' }), {
      target: { value: '60' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Save shared limits' }));
    await screen.findByText('Shared limits saved.');
    rerender(
      <ManagedKeyLimitsControl
        {...props}
        donation={{ ...donation, revision: '8' }}
        item={{ ...item, key_limits_revision: '6', max_rpm: 60 }}
      />,
    );
    expect(screen.getByRole('button', { name: 'Save shared limits' })).toBeDisabled();
    rerender(
      <ManagedKeyLimitsControl
        {...props}
        donation={{ ...donation, revision: '9' }}
        item={{ ...item, key_limits_revision: '7', max_rpm: 70 }}
      />,
    );
    expect(screen.getByRole('spinbutton', { name: 'Maximum RPM' })).toHaveValue(70);
    expect(screen.getByRole('button', { name: 'Save shared limits' })).toBeDisabled();
  });

  it('rejects fractional and out-of-range input and keeps unknown physical limits read only', async () => {
    patch.mockReset();
    const { props, rerender } = await setup();
    const rpm = screen.getByRole('spinbutton', { name: 'Maximum RPM' });
    for (const value of ['1.5', '2147483648', '-1', '']) {
      fireEvent.change(rpm, { target: { value } });
      expect(screen.getByRole('button', { name: 'Save shared limits' })).toBeDisabled();
    }
    rerender(
      <ManagedKeyLimitsControl
        {...props}
        item={{ ...item, key_limits_revision: null, max_rpm: null }}
      />,
    );
    expect(screen.getByRole('spinbutton', { name: 'Maximum RPM' })).toBeDisabled();
    expect(patch).not.toHaveBeenCalled();
  });
});
