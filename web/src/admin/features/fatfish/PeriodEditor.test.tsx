import { screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../test/unit/support';
import { PeriodManager } from './PeriodEditor';

const api = vi.hoisted(() => ({
  listPeriods: vi.fn(), getPeriod: vi.fn(), getNode: vi.fn(),
  validatePeriod: vi.fn(), savePeriod: vi.fn(), saveNode: vi.fn(),
  changePeriodState: vi.fn(), getVersion: vi.fn(), listLevels: vi.fn(), listVersions: vi.fn(),
}));
vi.mock('./api', () => ({ ...api }));
vi.mock('./useDraftGuard', () => ({ useDraftGuard: () => () => true }));

const period = {
  id: 'ffp_period', title: 'Autumn', description: 'Description', state: 'draft' as const,
  visible: false, paused: false, past_public: true,
  starts_at: Date.parse('2030-10-01T00:00:00Z') / 1000,
  ends_at: Date.parse('2030-11-01T00:00:00Z') / 1000,
  revision: '1', leaderboard_final: false,
  nodes: [{ id: 'ffn_one', period_id: 'ffp_period', title: 'First node', description: '',
    map_x: 200, map_y: 200, order: 0, revision: '1', version_id: 'ffv_one', hidden: false }],
};

describe('Fat Fish period editor', () => {
  beforeEach(() => {
    api.listPeriods.mockResolvedValue({ items: [period], page: 1, page_size: 20, has_more: false });
    api.getPeriod.mockResolvedValue(period);
    api.getNode.mockResolvedValue({ ...period.nodes[0], condition: {}, amounts: { unlock_cost: '0', ticket_price: '0', first_clear_reward: '0', star_rewards: ['0', '0', '0'] } });
    api.validatePeriod.mockResolvedValue({ publishable: false, reachable: ['ffn_one'], unreachable: [], missing_playtests: ['ffn_one'] });
    api.changePeriodState.mockReset();
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      if (String(input).endsWith('/time-context')) return new Response(JSON.stringify({ mode: 'site', offset_minutes: 0 }), { status: 200, headers: { 'Content-Type': 'application/json' } });
      throw new Error(`Unexpected fetch: ${String(input)}`);
    }));
  });
  it('previews missing playtest proof and refuses publish until the graph is valid', async () => {
    api.changePeriodState.mockResolvedValue({ ...period, state: 'open', revision: '2' });
    const view = await renderWithProviders(<PeriodManager />, { station: 'admin', role: 'admin' });
    view.queryClient.setQueryData(['admin', 'session'], { admin: { username: 'fixture-admin' } });
    await view.user.click(await screen.findByRole('button', { name: /Autumn · draft/ }));
    await view.user.click(await screen.findByRole('button', { name: 'Check and preview publish conditions' }));
    expect(await screen.findByText(/Missing one-star playtest.*First node/)).toBeInTheDocument();
    await view.user.click(screen.getByRole('button', { name: 'Publish period' }));
    expect(api.changePeriodState).not.toHaveBeenCalled();
    api.validatePeriod.mockResolvedValue({ publishable: true, reachable: ['ffn_one'], unreachable: [], missing_playtests: [] });
    await view.user.click(screen.getByRole('button', { name: 'Publish period' }));
    await waitFor(() => expect(api.changePeriodState).toHaveBeenCalledTimes(1));
    expect(api.changePeriodState.mock.calls[0].slice(0, 3)).toEqual(['ffp_period', 'publish', '1']);
  });
});
