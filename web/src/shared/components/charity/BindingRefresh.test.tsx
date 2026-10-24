import { screen, waitFor, within } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../test/unit/support';
import {
  charityKeys,
  normalizeAdminCharityModel,
  type CharityBindingCandidate,
} from '@shared/operations/charity';
import * as pages from '@shared/operations/charityPages';
import { ModelsPanel } from './CharityModels';
import { CharityTargets } from './CharityTargets';

vi.mock('../AsyncLeaveDialog', () => ({ AsyncLeaveDialog: () => null }));
vi.mock('./BindingAddForm', () => ({ BindingAddForm: () => null }));

const model = {
  id: '31',
  provider: 'Provider',
  model: 'binding-test',
  full_name: '[公益]Provider/binding-test',
  enabled: true,
  model_types: ['embeddings'],
  route_strategy: 'ordered',
  allowed_levels: [1],
  public_description: '',
  transport_rule: 'passthrough',
  flatten_tool_calls: false,
  cloudflare_proxy_enabled: false,
  cloudflare_proxy_mode: 'legacy',
  pricing: { mode: 'per_request', user_price: '1', donor_reward: '0' },
  discount: { enabled: false, percent: 0, start_at: null, end_at: null },
  recurring_discount: { enabled: false, time_zone: 'UTC', default_percent: 100, periods: [] },
  effective_discount: { source: 'none', percent: 100, next_change_at: null },
  server_now: 1800000000,
  revision: '1',
  binding_revision: '1',
  binding_count: '2',
  rolling_success: { sample_count: '0', success_count: '0', percent: null },
  created_at: 1,
  updated_at: 1,
};
const source = {
  connector_type: 'openai-compatible',
  canonical_base_url: 'https://example.test/v1',
  display_head: 'head',
  display_tail: 'tail',
};
const binding = (id: string, ord: number) => ({
  id,
  ord,
  donation_key_id: '21',
  donation_id: '7',
  upstream_model_id: `upstream-${id}`,
  source,
  source_types: ['automatic'],
});
const candidate = {
  donation_key_id: '21',
  donation_id: '7',
  upstream_model_id: 'new-upstream',
  source,
  source_types: ['automatic'],
  native_services: ['chat_completions'],
  capabilities: { can_bind: true },
} as CharityBindingCandidate;
const json = (body: unknown, status = 200) => Response.json(body, { status });

function fixture(
  options: { unknown?: boolean; refreshFailure?: boolean; conflict?: boolean } = {},
) {
  let rows = [binding('41', 0), binding('42', 1)];
  let revision = '1';
  let reads = 0;
  let failedRefresh = false;
  const writes: { method: string; body: Record<string, unknown>; key: string | null }[] = [];
  vi.spyOn(pages, 'getManagedBindingCandidatesPage').mockResolvedValue({
    data: [candidate],
    next_cursor: null,
    pagination: { page: '1', page_size: 20, total_items: '1', total_pages: '1' },
  });
  vi.stubGlobal(
    'fetch',
    vi.fn<typeof fetch>(async (input, init) => {
      const url = new URL(String(input), window.location.origin);
      const method = init?.method ?? 'GET';
      if (method === 'GET') {
        if (url.pathname.endsWith('/bindings')) {
          reads++;
          if (options.refreshFailure && writes.length && !failedRefresh) {
            failedRefresh = true;
            throw new TypeError('Failed to fetch');
          }
          return json({ bindings: rows, binding_revision: revision });
        }
        if (url.pathname.endsWith('/charity-models/31'))
          return json({ ...model, binding_revision: revision, binding_count: String(rows.length) });
        if (url.pathname.endsWith('/time-context'))
          return json({ mode: 'site', offset_minutes: 0 });
        if (url.pathname.endsWith('/time-zones'))
          return json({ version: 'go1.26.9-zoneinfo', zones: ['UTC'] });
        throw new Error(`Unexpected GET ${url.pathname}`);
      }
      const body = JSON.parse(String(init?.body)) as Record<string, unknown>;
      writes.push({ method, body, key: new Headers(init?.headers).get('Idempotency-Key') });
      if (options.unknown && writes.length <= 2) throw new TypeError('Failed to fetch');
      if (options.conflict && writes.length === 1) {
        revision = '2';
        return json({ error: { code: 'conflict', message: 'Changed.' } }, 409);
      }
      revision = String(Number(revision) + 1);
      if (url.pathname.endsWith('/order'))
        rows = (body.order as string[]).map((id, ord) => binding(id, ord));
      else if (method === 'DELETE')
        rows = rows
          .filter((row) => !url.pathname.endsWith('/' + row.id))
          .map((row, ord) => ({ ...row, ord }));
      else rows = [...rows, binding('43', rows.length)];
      return json({
        model_id: '31',
        binding_revision: revision,
        added_count: '1',
        binding_count: String(rows.length),
      });
    }),
  );
  return { writes, reads: () => reads };
}

async function setup(targetOnly = false) {
  const view = await renderWithProviders(
    targetOnly ? (
      <CharityTargets
        role="admin"
        accountId="test-admin"
        model={normalizeAdminCharityModel(model)}
      />
    ) : (
      <ModelsPanel
        role="admin"
        accountId="test-admin"
        renderKeyManager={() => null}
        renderScopedKeys={() => null}
      />
    ),
    {
      station: 'admin',
      role: 'admin',
      locale: 'en',
      route: '/charity?charity_section=models&charity_model=31',
    },
  );
  view.queryClient.setQueryData(['admin', 'session'], { admin: { username: 'synthetic-admin' } });
  return view;
}

async function selectTarget(view: Awaited<ReturnType<typeof setup>>) {
  const summary = await screen.findByText('Add target group', { selector: 'summary strong' });
  const group = summary.closest('details')!;
  await view.user.click(summary);
  await view.user.click(await within(group).findByRole('button', { name: 'Add target' }));
  await view.user.click(within(group).getByRole('checkbox'));
  return group;
}

afterEach(() => vi.unstubAllGlobals());

it.each(['order', 'delete'] as const)(
  'reads bindings once after a confirmed %s receipt',
  async (action) => {
    const f = fixture();
    const view = await setup();
    await screen.findByText('upstream-41');
    const before = f.reads();
    if (action === 'order') {
      await view.user.click(screen.getAllByRole('button', { name: 'Down' })[0]!);
      await view.user.click(screen.getByRole('button', { name: 'Save connection order' }));
    } else await view.user.click(screen.getAllByRole('button', { name: 'Remove' })[0]!);
    await waitFor(() => expect(f.writes).toHaveLength(1));
    await waitFor(() => expect(f.reads()).toBe(before + 1));
    await waitFor(() => expect(screen.getAllByRole('button', { name: 'Remove' })[0]).toBeEnabled());
    expect(f.reads()).toBe(before + 1);
    const saved = view.queryClient.getQueryData<{ binding_revision: string }>([
      ...charityKeys.bindings('admin', '31'),
      'test-admin',
    ]);
    expect(saved?.binding_revision).toBe('2');
  },
);

it('reads once and advances the retained target revision after a confirmed edit', async () => {
  const f = fixture();
  const view = await setup(true);
  const group = await selectTarget(view);
  const before = f.reads();
  await view.user.click(within(group).getByRole('button', { name: 'Add target group' }));
  await within(group).findByText('Saved');
  expect(f.reads()).toBe(before + 1);
  expect(
    view.queryClient.getQueryData<{ binding_revision: string }>([
      ...charityKeys.bindings('admin', '31'),
      'test-admin',
    ])?.binding_revision,
  ).toBe('2');
});

it('retries an uncertain target write with the same body and key before one authoritative read', async () => {
  const f = fixture({ unknown: true });
  const view = await setup(true);
  const group = await selectTarget(view);
  const before = f.reads();
  await view.user.click(within(group).getByRole('button', { name: 'Add target group' }));
  const retry = await within(group).findByRole('button', { name: 'Check saved result' });
  expect(f.writes).toHaveLength(2);
  expect(f.reads()).toBe(before);
  await view.user.click(retry);
  await within(group).findByText('Saved');
  expect(f.writes).toHaveLength(3);
  expect(f.writes.map((write) => write.body)).toEqual(Array(3).fill(f.writes[0]!.body));
  expect(new Set(f.writes.map((write) => write.key)).size).toBe(1);
  expect(f.reads()).toBe(before + 1);
});

it('retries a failed target refresh without repeating the confirmed write', async () => {
  const f = fixture({ refreshFailure: true });
  const view = await setup(true);
  const group = await selectTarget(view);
  await view.user.click(within(group).getByRole('button', { name: 'Add target group' }));
  await view.user.click(await within(group).findByRole('button', { name: 'Retry' }));
  await within(group).findByText('Saved');
  expect(f.writes).toHaveLength(1);
  expect(f.reads()).toBe(3);
});

it('keeps a conflicting target draft and adopts the refreshed revision before a new write', async () => {
  const f = fixture({ conflict: true });
  const view = await setup(true);
  const group = await selectTarget(view);
  await view.user.click(within(group).getByRole('button', { name: 'Add target group' }));
  await view.user.click(
    await within(group).findByRole('button', { name: 'Continue with current revision' }),
  );
  expect(within(group).getByRole('checkbox')).toBeChecked();
  await view.user.click(within(group).getByRole('button', { name: 'Add target group' }));
  await within(group).findByText('Saved');
  expect(f.writes.map((write) => write.body.expected_binding_revision)).toEqual(['1', '2']);
  expect(f.reads()).toBe(3);
});
