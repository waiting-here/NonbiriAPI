import { screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { AdminDonation } from '@shared/operations/charity';
import { renderWithProviders } from '../../../../test/unit/support';
import { numberedPage } from '../../../../test/e2e/numbered-fixtures';
import { DonationPresetApply } from './DonationPresetApply';
vi.mock('../AsyncLeaveDialog', () => ({ AsyncLeaveDialog: () => null }));

const channel = `mch_${'A'.repeat(22)}`;
const item = {
  id: '7',
  revision: '4',
  status: 'approved',
  keys: [
    {
      id: '21',
      safe_note: 'Primary donated key',
      safe_source: {
        kind: 'mainstream',
        channel_id: channel,
        name: 'Example channel',
        connector_type: 'openai-compatible',
        base_url: 'https://example.test',
      },
    },
  ],
} as AdminDonation;
const mappings = [
  {
    upstream_model_id: 'exact/up-a',
    charity_model_id: '31',
    full_name: 'Original target A',
    binding_revision: '1',
  },
  {
    upstream_model_id: 'exact/up-b',
    charity_model_id: '32',
    full_name: 'Original target B',
    binding_revision: '2',
  },
];
const json = (value: unknown) =>
  new Response(JSON.stringify(value), { headers: { 'content-type': 'application/json' } });
describe('donation preset result snapshots', () => {
  it('browses custom-source candidates, keeps the search on return, and retries the chosen preset intact', async () => {
    const custom = {
      ...item,
      keys: item.keys.map((key) => ({
        ...key,
        safe_source: {
          kind: 'custom' as const,
          connector_type: 'openai-compatible' as const,
          base_url: 'https://donor.example.test/v1',
        },
      })),
    };
    const candidates = Array.from({ length: 21 }, (_, index) => ({
      channel_id: `mch_${'B'.repeat(19)}${String(index).padStart(2, '0')}A`,
      name: index === 0 ? 'Matching channel' : `Other template ${index}`,
      connector_type: 'openai-compatible',
      base_url: 'https://channel.example.test/v1',
      enabled: true,
      state: 'active',
      channel_revision: '1',
      preset_revision: '1',
      mapping_count: '2',
      recommended: index === 0,
    }));
    const reads: URL[] = [];
    const writes: { body: unknown; key: string | null }[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn<typeof fetch>(async (input, init) => {
        const url = new URL(String(input), 'https://example.test');
        if ((init?.method ?? 'GET') === 'GET') {
          if (url.pathname.endsWith('/charity-binding-presets')) {
            reads.push(url);
            const query = url.searchParams.get('q') ?? '';
            return json(
              numberedPage(
                candidates.filter((row) => row.name.includes(query)),
                url.searchParams,
              ),
            );
          }
          return json({
            channel_id: url.pathname.split('/').at(-1),
            channel_revision: '1',
            preset_revision: '1',
            mappings,
          });
        }
        writes.push({
          body: JSON.parse(String(init?.body)),
          key: new Headers(init?.headers).get('Idempotency-Key'),
        });
        if (writes.length === 1) throw new TypeError('response lost');
        return json({
          items: [{ donation_key_id: '21', results: ['missing_model', 'already_bound'] }],
          models: [],
          added_count: '0',
        });
      }),
    );
    const view = await renderWithProviders(
      <DonationPresetApply role="admin" accountId="fixture-admin" item={custom} />,
      { station: 'admin' },
    );
    view.queryClient.setQueryData(['admin', 'session'], { admin: { username: 'fixture-admin' } });
    expect(reads).toHaveLength(0);
    await view.user.click(screen.getByText('Apply channel preset'));
    expect(await screen.findByText('Has a matching endpoint')).toBeVisible();
    await view.user.click(screen.getByRole('button', { name: 'Next' }));
    const last = await screen.findByRole('row', { name: /Other template 20/ });
    await view.user.click(within(last).getByRole('button', { name: 'Select preset' }));
    expect(await screen.findByText(/Use 2 mappings from Other template 20/)).toBeVisible();
    await view.user.click(screen.getByRole('button', { name: 'Change preset' }));
    expect(await screen.findByRole('row', { name: /Other template 20/ })).toBeVisible();
    await view.user.type(
      screen.getByRole('searchbox', { name: 'Search channels' }),
      'Other template 20',
    );
    await view.user.click(screen.getByRole('button', { name: 'Search' }));
    await view.user.click(
      within(await screen.findByRole('row', { name: /Other template 20/ })).getByRole('button', {
        name: 'Select preset',
      }),
    );
    await view.user.click(await screen.findByRole('button', { name: 'Apply preset to all keys' }));
    await screen.findByRole('button', { name: 'Retry the original action' });
    expect(screen.getByRole('button', { name: 'Change preset' })).toBeDisabled();
    await view.user.click(screen.getByRole('button', { name: 'Retry the original action' }));
    expect(await screen.findByRole('region', { name: 'Binding results' })).toHaveTextContent(
      'Already bound',
    );
    expect(writes).toHaveLength(2);
    expect(writes[1]).toEqual(writes[0]);
    expect(writes[0]?.body).toMatchObject({ channel_id: candidates[20]!.channel_id });
    expect(reads.every((url) => url.searchParams.get('donation_id') === item.id)).toBe(true);
    expect(reads.some((url) => url.searchParams.get('page') === '2')).toBe(true);
    expect(
      reads.some(
        (url) =>
          url.searchParams.get('q') === 'Other template 20' && url.searchParams.get('page') === '1',
      ),
    ).toBe(true);
  });

  it('labels an unknown retry with its original preset order after the preset has changed', async () => {
    const writes: { body: unknown; key: string | null }[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn<typeof fetch>(async (_input, init) => {
        if ((init?.method ?? 'GET') === 'GET')
          return json({
            channel_id: channel,
            channel_revision: '1',
            preset_revision: writes.length ? '2' : '1',
            mappings: writes.length ? [...mappings].reverse() : mappings,
          });
        writes.push({
          body: JSON.parse(String(init?.body)),
          key: new Headers(init?.headers).get('Idempotency-Key'),
        });
        if (writes.length === 1) throw new TypeError('response lost');
        return json({
          items: [{ donation_key_id: '21', results: ['missing_model', 'already_bound'] }],
          models: [
            { charity_model_id: '31', binding_revision: '1' },
            { charity_model_id: '32', binding_revision: '2' },
          ],
          added_count: '0',
        });
      }),
    );
    const view = await renderWithProviders(
      <DonationPresetApply role="admin" accountId="fixture-admin" item={item} />,
      { station: 'admin' },
    );
    view.queryClient.setQueryData(['admin', 'session'], { admin: { username: 'fixture-admin' } });
    await view.user.click(await screen.findByRole('button', { name: 'Apply preset to all keys' }));
    await view.user.click(await screen.findByRole('button', { name: 'Retry the original action' }));
    const results = await screen.findByRole('region', { name: 'Binding results' });
    expect(within(results).getByRole('row', { name: /Original target A/ })).toHaveTextContent(
      'exact/up-a',
    );
    expect(within(results).getByRole('row', { name: /Original target A/ })).toHaveTextContent(
      'Upstream model not found',
    );
    expect(within(results).getByRole('row', { name: /Original target B/ })).toHaveTextContent(
      'Already bound',
    );
    expect(writes).toHaveLength(2);
    expect(writes[1]).toEqual(writes[0]);
    expect(writes[0]?.body).toMatchObject({ expected_preset_revision: '1' });
  });
});
