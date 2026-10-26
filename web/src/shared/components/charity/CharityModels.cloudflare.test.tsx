vi.mock('@shared/components/AsyncLeaveDialog', () => ({ AsyncLeaveDialog: () => null }));
import { screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../test/unit/support';
import { ModelsPanel } from './CharityModels';
import {
  normalizeAdminCharityModel,
  normalizeStewardCharityModel,
} from '@shared/operations/charity';

const original = {
  recurring_discount: { enabled: false, time_zone: 'UTC', default_percent: 100, periods: [] },
  effective_discount: { source: 'none', percent: 100, next_change_at: null },
  server_now: 1800000000,
  model_types: ['embeddings'],
  route_strategy: 'cache_balanced',
  id: '31',
  provider: 'Provider',
  model: 'model',
  full_name: '[公益]Provider/model',
  enabled: true,
  allowed_levels: [1, 2, 3, 4, 5],
  public_description: '',
  pricing: { mode: 'per_request', user_price: '1', donor_reward: '0' },
  discount: { enabled: false, percent: 0, start_at: null, end_at: null },
  transport_rule: 'passthrough',
  flatten_tool_calls: false,
  cloudflare_proxy_enabled: false,
  cloudflare_proxy_mode: 'legacy',
  revision: '1',
  binding_revision: '0',
  binding_count: '0',
  rolling_success: { sample_count: '0', success_count: '0', percent: null },
  created_at: 1,
  updated_at: 1,
};
const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
function fixture(
  role: 'admin' | 'steward',
  conflict = false,
  options: { headers?: boolean; unknown?: boolean } = {},
) {
  let current = {
    ...original,
    session_headers: options.headers
      ? [{ name: 'x-session-id', idle_seconds: 300, format: 'hex_32' }]
      : [],
  };
  const requests: RequestInit[] = [];
  const sent: Record<string, unknown>[] = [];
  const base = role === 'admin' ? '/admin/api' : '/api/steward';
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input, init) => {
      const url = new URL(String(input), window.location.origin);
      if (url.pathname.endsWith('/time-context')) return json({ mode: 'site', offset_minutes: 0 });
      if (url.pathname.endsWith('/bindings')) return json({ bindings: [], binding_revision: '0' });
      if (url.pathname.endsWith('/binding-candidates'))
        return json({
          data: [],
          next_cursor: null,
          pagination: { page: '1', page_size: 10, total_items: '0', total_pages: '1' },
        });
      if (init?.method === 'PATCH' || init?.method === 'POST') {
        const body = JSON.parse(String(init.body));
        sent.push(body);
        requests.push(init);
        if (options.unknown && sent.length === 1) throw new TypeError('Failed to fetch');
        if (conflict && sent.length === 1) {
          current = { ...current, revision: '2', cloudflare_proxy_enabled: true };
          return json({ error: { code: 'conflict', message: 'Changed.' } }, 409);
        }
        current = {
          ...current,
          ...body,
          revision: '3',
          full_name: `[公益]${body.provider}/${body.model}`,
        };
        delete (current as Record<string, unknown>).expected_revision;
        return json(current, init.method === 'POST' ? 201 : 200);
      }
      if (url.pathname === base + '/charity-models/31') return json(current);
      throw new Error('Unexpected request: ' + url.pathname);
    }),
  );
  return { sent, requests };
}
async function editor(role: 'admin' | 'steward', create = false, trainee = false) {
  const view = await renderWithProviders(
    <ModelsPanel
      role={role}
      accountId="1"
      renderKeyManager={() => null}
      renderScopedKeys={() => null}
    />,
    {
      station: role === 'admin' ? 'admin' : 'user',
      role: trainee ? 'level5' : role === 'admin' ? 'admin' : 'level6',
      route:
        '/charity?charity_section=models&' + (create ? 'model_workspace=new' : 'charity_model=31'),
    },
  );
  view.queryClient.setQueryData(
    [role === 'admin' ? 'admin' : 'user', 'session'],
    role === 'admin'
      ? { admin: { username: 'fixture-admin' } }
      : { user: { id: '1', username: 'fixture-steward', effective_level: trainee ? 5 : 6 } },
  );
  return view;
}
afterEach(() => vi.unstubAllGlobals());
describe('managed model Cloudflare setting', () => {
  it.each(['admin', 'steward'] as const)(
    '%s reads, saves, reloads and recovers a conflicting draft',
    async (role) => {
      const f = fixture(role, true);
      const view = await editor(role);
      const toggle = await screen.findByRole('switch', { name: 'Cloudflare forwarding' });
      expect(toggle).not.toBeChecked();
      await view.user.click(toggle);
      await view.user.click(screen.getByRole('button', { name: 'Save model #31' }));
      await waitFor(() => expect(f.sent).toHaveLength(1));
      expect(f.sent[0]).toMatchObject({ expected_revision: '1', cloudflare_proxy_enabled: true });
      expect(toggle).toBeChecked();
      const reload = screen.getByRole('button', { name: 'Discard edits and reload saved model' });
      await view.user.click(reload);
      expect(toggle).toBeChecked();
      await view.user.click(toggle);
      await view.user.click(screen.getByRole('button', { name: 'Save model #31' }));
      await waitFor(() => expect(f.sent).toHaveLength(2));
      expect(f.sent[1]).toMatchObject({ expected_revision: '2', cloudflare_proxy_enabled: false });
      expect(f.sent[1]).not.toHaveProperty('cloudflare_proxy_mode');
      await view.user.click(reload);
      expect(toggle).not.toBeChecked();
    },
  );
  it.each(['admin', 'steward'] as const)('%s defaults new models off', async (role) => {
    fixture(role);
    await editor(role, true);
    expect(screen.getByRole('switch', { name: 'Cloudflare forwarding' })).not.toBeChecked();
  });
  it('shows level 5 the shared model editor without starting a write', async () => {
    const f = fixture('steward');
    await editor('steward', false, true);
    expect(await screen.findByText('Off. Requests connect directly to upstreams.')).toBeVisible();
    expect(screen.getByRole('switch', { name: 'Cloudflare forwarding' })).toBeVisible();
    expect(f.sent).toHaveLength(0);
  });
  it.each([normalizeAdminCharityModel, normalizeStewardCharityModel])(
    'strictly reads bool and mode without deriving either',
    (normalize) => {
      expect(normalize(original)).toMatchObject({
        cloudflare_proxy_enabled: false,
        cloudflare_proxy_mode: 'legacy',
      });
      for (const value of [undefined, null, 'false', 0])
        expect(() => normalize({ ...original, cloudflare_proxy_enabled: value })).toThrow();
      for (const value of [undefined, null, 'unknown'])
        expect(() => normalize({ ...original, cloudflare_proxy_mode: value })).toThrow();
    },
  );
});

describe('managed model session header settings', () => {
  it.each(['admin', 'steward'] as const)(
    '%s saves zero and OpenCode formats, restores saved values after a conflict, and clears',
    async (role) => {
      const f = fixture(role, true, { headers: true });
      const view = await editor(role);
      const idle = await screen.findByLabelText('Idle expiry (seconds)');
      expect(idle).toHaveValue(300);
      expect(screen.getByLabelText('Random value format')).toHaveValue('hex_32');
      await view.user.clear(idle);
      await view.user.type(idle, '0');
      await view.user.selectOptions(
        screen.getByLabelText('Random value format'),
        'opencode_message',
      );
      await view.user.click(screen.getByRole('button', { name: 'Save model #31' }));
      await waitFor(() => expect(f.sent).toHaveLength(1));
      expect(f.sent[0].session_headers).toEqual([
        { name: 'x-session-id', idle_seconds: 0, format: 'opencode_message' },
      ]);
      expect(idle).toHaveValue(0);
      await view.user.click(
        screen.getByRole('button', { name: 'Discard edits and reload saved model' }),
      );
      expect(idle).toHaveValue(300);
      expect(screen.getByLabelText('Random value format')).toHaveValue('hex_32');
      await view.user.click(screen.getByRole('button', { name: 'Remove header 1' }));
      await view.user.click(screen.getByRole('button', { name: 'Save model #31' }));
      await waitFor(() => expect(f.sent).toHaveLength(2));
      expect(f.sent[1]).toMatchObject({ expected_revision: '2', session_headers: [] });
    },
  );

  it.each(['admin', 'steward'] as const)(
    '%s keeps an unresolved header draft disabled and retries the exact saved request',
    async (role) => {
      const f = fixture(role, false, { headers: true, unknown: true });
      const view = await editor(role);
      const idle = await screen.findByLabelText('Idle expiry (seconds)');
      await view.user.clear(idle);
      await view.user.type(idle, '0');
      await view.user.selectOptions(screen.getByLabelText('Random value format'), 'opencode_part');
      await view.user.click(screen.getByRole('button', { name: 'Save model #31' }));
      await waitFor(() => expect(idle).toBeDisabled());
      expect(idle).toHaveValue(0);
      expect(screen.getByLabelText('Random value format')).toHaveValue('opencode_part');
      const retry = await screen.findByRole('button', { name: 'Retry the last save' });
      await view.user.click(retry);
      await waitFor(() => expect(f.sent).toHaveLength(2));
      expect(f.sent[1]).toEqual(f.sent[0]);
      expect(new Headers(f.requests[1].headers).get('Idempotency-Key')).toBe(
        new Headers(f.requests[0].headers).get('Idempotency-Key'),
      );
    },
  );
});
