import { useEffect } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { act, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { CharityModel } from '@shared/operations/charity';
import { charityKeys } from '@shared/operations/charity';
import type { CharityBindingPicker, CharitySelection } from '../CharityBindingPicker';
import { renderWithProviders } from '../../../../test/unit/support';
import { BindingAddForm } from './BindingAddForm';

// Pagination and real candidate selection are exercised by CharityBindingPicker.test.
vi.mock('../CharityBindingPicker', () => ({
  CharityBindingPicker: ({
    selected,
    onChange,
    locked,
    onReadStateChange,
  }: Parameters<typeof CharityBindingPicker>[0]) => {
    useEffect(() => onReadStateChange?.(false), [onReadStateChange]);
    const entry: CharitySelection = {
      donation_key_id: '21',
      donation_id: '7',
      upstream_model_id: 'provider/model-2026-10',
      source_types: ['automatic'],
      source: {
        connector_type: 'openai-compatible',
        canonical_base_url: 'https://example.test/v1',
        display_head: 'head',
        display_tail: 'tail',
      },
      note: '',
    };
    return (
      <label>
        <input
          type="checkbox"
          checked={Boolean(selected['21:provider/model-2026-10'])}
          disabled={locked}
          onChange={(event) =>
            onChange(event.target.checked ? { '21:provider/model-2026-10': entry } : {})
          }
        />
        Candidate model
      </label>
    );
  },
}));

const model: CharityModel = {
  recurring_discount: { enabled: false, time_zone: 'UTC', default_percent: 100, periods: [] },
  effective_discount: { source: 'none', percent: 100, next_change_at: null },
  server_now: 1800000000,

  id: '31',
  provider: 'Provider',
  model: 'target',
  full_name: '[公益]Provider/target',
  enabled: true,
  model_types: ['chat_completions'],
  route_strategy: 'ordered',
  allowed_levels: [1],
  public_description: '',
  transport_rule: 'passthrough',
  cloudflare_proxy_enabled: false,
  cloudflare_proxy_mode: 'direct',
  flatten_tool_calls: false,
  pricing: { mode: 'per_request', user_price: '1', donor_reward: '0' },
  token_reserve_credits: null,
  discount: { enabled: false, percent: 0, start_at: null, end_at: null },
  revision: '1',
  binding_revision: '1',
  binding_count: '0',
  rolling_success: { sample_count: '0', success_count: '0', percent: null },
  created_at: 1,
  updated_at: 1,
};
const json = (value: unknown, status = 200) =>
  new Response(JSON.stringify(value), { status, headers: { 'content-type': 'application/json' } });
function Harness() {
  const client = useQueryClient();
  return (
    <BindingAddForm
      role="admin"
      accountId="fixture-admin"
      model={model}
      refresh={() =>
        client.invalidateQueries({ queryKey: charityKeys.root('admin') }, { throwOnError: true })
      }
    />
  );
}

describe('compact binding write recovery', () => {
  it('retries an unknown result with the original body and key after an authority refresh', async () => {
    const writes: { body: unknown; key: string | null }[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn<typeof fetch>(async (input, init) => {
        const path = new URL(String(input), 'https://example.test').pathname;
        if ((init?.method ?? 'GET') === 'GET')
          return json({ bindings: [], binding_revision: writes.length ? '2' : '1' });
        expect(path).toBe('/admin/api/charity-models/31/bindings/batch-receipt');
        writes.push({
          body: JSON.parse(String(init?.body)),
          key: new Headers(init?.headers).get('Idempotency-Key'),
        });
        if (writes.length === 1) throw new TypeError('synthetic network interruption');
        return json(
          { model_id: '31', binding_revision: '2', added_count: '1', binding_count: '1' },
          201,
        );
      }),
    );
    const view = await renderWithProviders(<Harness />, { station: 'admin' });
    view.queryClient.setQueryData(['admin', 'session'], { admin: { username: 'fixture-admin' } });
    await view.user.click(await screen.findByRole('checkbox', { name: 'Candidate model' }));
    await view.user.click(screen.getByRole('button', { name: 'Add 1 connections' }));
    const retry = await screen.findByRole('button', { name: 'Retry the original action' });
    expect(screen.getByRole('button', { name: 'Add 1 connections' })).toBeDisabled();
    expect(screen.getByRole('checkbox', { name: 'Candidate model' })).toBeChecked();
    expect(screen.getByRole('checkbox', { name: 'Candidate model' })).toBeDisabled();
    await view.user.click(retry);
    await screen.findByText('Connections saved.');
    expect(writes).toHaveLength(2);
    expect(writes[1]).toEqual(writes[0]);
    expect(writes[0]?.key).toBeTruthy();
    expect(writes[0]?.body).toEqual({
      expected_binding_revision: '1',
      selections: [{ donation_key_id: '21', upstream_model_id: 'provider/model-2026-10' }],
    });
  });

  it('keeps the original selection revision through background data changes until explicit reread', async () => {
    let revision = '1';
    const revisions: string[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn<typeof fetch>(async (_input, init) => {
        if ((init?.method ?? 'GET') === 'GET')
          return json({ bindings: [], binding_revision: revision });
        const body = JSON.parse(String(init?.body)) as { expected_binding_revision: string };
        revisions.push(body.expected_binding_revision);
        if (revisions.length === 1)
          return json({ error: { code: 'conflict', message: 'Connections changed.' } }, 409);
        return json(
          { model_id: '31', binding_revision: '3', added_count: '1', binding_count: '1' },
          201,
        );
      }),
    );
    const view = await renderWithProviders(<Harness />, { station: 'admin' });
    view.queryClient.setQueryData(['admin', 'session'], { admin: { username: 'fixture-admin' } });
    await view.user.click(await screen.findByRole('checkbox', { name: 'Candidate model' }));
    revision = '2';
    act(() =>
      view.queryClient.setQueryData([...charityKeys.bindings('admin', '31'), 'fixture-admin'], {
        bindings: [],
        binding_revision: revision,
      }),
    );
    await view.user.click(screen.getByRole('button', { name: 'Add 1 connections' }));
    const reread = await screen.findByRole('button', { name: 'Refresh and keep selections' });
    expect(revisions).toEqual(['1']);
    expect(screen.getByRole('checkbox', { name: 'Candidate model' })).toBeChecked();
    await view.user.click(reread);
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Add 1 connections' })).toBeEnabled(),
    );
    await view.user.click(screen.getByRole('button', { name: 'Add 1 connections' }));
    await screen.findByText('Connections saved.');
    expect(revisions).toEqual(['1', '2']);
  });

  it('only rereads after confirmed save when the following read fails', async () => {
    let writes = 0;
    let readsFail = false;
    vi.stubGlobal(
      'fetch',
      vi.fn<typeof fetch>(async (_input, init) => {
        if ((init?.method ?? 'GET') === 'GET') {
          if (readsFail) throw new TypeError('synthetic read failure');
          return json({ bindings: [], binding_revision: writes ? '2' : '1' });
        }
        writes += 1;
        readsFail = true;
        return json(
          { model_id: '31', binding_revision: '2', added_count: '1', binding_count: '1' },
          201,
        );
      }),
    );
    const view = await renderWithProviders(<Harness />, { station: 'admin' });
    view.queryClient.setQueryData(['admin', 'session'], { admin: { username: 'fixture-admin' } });
    await view.user.click(await screen.findByRole('checkbox', { name: 'Candidate model' }));
    await view.user.click(screen.getByRole('button', { name: 'Add 1 connections' }));
    const retry = await screen.findByRole('button', { name: 'Retry' });
    await screen.findByText('Connections saved.');
    expect(screen.getByRole('button', { name: 'Add 0 connections' })).toBeDisabled();
    expect(
      screen.queryByRole('button', { name: 'Retry the original action' }),
    ).not.toBeInTheDocument();
    readsFail = false;
    await view.user.click(retry);
    await waitFor(() =>
      expect(screen.getByRole('checkbox', { name: 'Candidate model' })).toBeEnabled(),
    );
    expect(writes).toBe(1);
  });
});
