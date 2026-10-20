import { screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { AdminDonation } from '@shared/operations/charity';
import { renderWithProviders } from '../../../../test/unit/support';
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
