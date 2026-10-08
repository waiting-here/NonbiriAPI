import { act, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../test/unit/support';
import { FatFishSessionController } from '../../../shared/fatfish/session';
import type { FatFishChallenge } from '../../../shared/fatfish/api';
import { FatFishActivityPage } from './FatFishActivity';

const session = { user: {
  id: '1', username: 'fixture-user', avatar: null, avatar_url: null, guild_nick: null,
  guild_avatar_url: null, lang: 'en', is_banned: false, banned_until: null,
  charity_suspended_until: null, endpoint_limit: null, effective_endpoint_limit: '10',
  rpm_limit: null, effective_rpm_limit: '60', concurrency_limit: null,
  effective_concurrency_limit: '5', balance: '0', game_balance: '0', donation_credit: '0',
  effective_level: 1, level_display_name: 'Lv1', game_profile_public: false,
  charity_profile_public: false, automatic_restrictions: [], created_at: 1700000000,
  updated_at: 1700000001, usage: { total_requests: '0', total_uncached_input_tokens: '0',
    total_cache_write_input_tokens: '0', total_cache_read_input_tokens: '0',
    total_output_tokens: '0', total_prompt_tokens: '0', total_completion_tokens: '0',
    total_unknown_usage_requests: '0' },
} };
const progress = { unlocked: false, passed: false, best_stars: 0, best_score_units: '0', best_at_ms: null };
const visible = { id: 'ffn_visible', period_id: 'ffp_one', title: 'Open tank', description: 'Feed one fish',
  map_x: 0, map_y: 0, order: 0, hidden: false, eligible: true, progress, revision: '1',
  content_hash: 'a'.repeat(64), amounts: { unlock_cost: '170141183460469231731687303715884105.727',
    ticket_price: '2', first_clear_reward: '3', star_rewards: ['1', '2', '3'] } };
const hidden = { id: 'ffn_hidden', period_id: 'ffp_one', map_x: 100, map_y: 100, order: 1,
  hidden: true, eligible: false, progress };
const period = { id: 'ffp_one', title: 'First season', description: 'A new tank', state: 'open',
  visible: true, paused: false, past_public: false, starts_at: 1700000000,
  ends_at: 253402300799, revision: '1', leaderboard_final: false, nodes: [visible, hidden] };
const reply = (body: unknown) => new Response(JSON.stringify(body), {
  status: 200, headers: { 'Content-Type': 'application/json' },
});
beforeEach(() => {
  vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(null);
});
afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); });

describe('fat fish user service projection', () => {
  it('keeps hidden node facts hidden, displays exact decimal fees, and pages history and boards', async () => {
    vi.spyOn(Date, 'now').mockReturnValue(1800000000000);
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input);
      if (path === '/api/session') return reply(session);
      if (path.includes('/challenges/current')) return reply(null);
      if (path.includes('/periods/ffp_one/nodes/ffn_visible')) return reply({ ...visible,
        condition_hint: { kind: 'all', met: false, hidden_count: 1,
          children: [{ kind: 'hidden', met: false, hidden_count: 1 }] } });
      if (path.includes('/periods/ffp_one/leaderboard')) {
        const page = Number(new URL(path, 'http://test').searchParams.get('page'));
        return reply({ period_id: 'ffp_one', final: false, page, page_size: 20, total: 21,
          rows: [{ rank: page === 1 ? '1' : '21', score_units: '9007199254740993',
            achieved_at_ms: 1800000000000, is_me: false, identity: { kind: 'anonymous' } }] });
      }
      if (path.includes('/periods/ffp_one')) return reply(period);
      if (path.includes('/periods?page=')) return reply({ items: [{ ...period, nodes: undefined }], page: 1, page_size: 20, has_more: false });
      if (path.includes('/history')) {
        const page = Number(new URL(path, 'http://test').searchParams.get('page'));
        return reply({ items: [{ id: `ffc_${page}`, state: 'settled_pass', ticket_price: '2',
          result: { passed: true, stars: page, score_units: '3', ticket_charge: '2', ticket_refund: '0', rewards: '5' } }],
        page, page_size: 20, has_more: page < 3 });
      }
      throw new Error(`Unexpected request ${path}`);
    }));
    const view = await renderWithProviders(<FatFishActivityPage />, {
      station: 'user', role: 'user', route: '/activities/fat-fish?period=ffp_one&node=ffn_visible',
    });
    expect(await screen.findByText('170141183460469231731687303715884105.727')).toBeInTheDocument();
    expect(screen.getByText('Hidden node')).toBeInTheDocument();
    expect(screen.getByText('Complete a hidden prerequisite')).toBeInTheDocument();
    expect(screen.queryByText('Secret tank')).not.toBeInTheDocument();
    expect(screen.getByText('90071992547409.93')).toBeInTheDocument();
    expect(screen.getByText('ffc_1')).toBeInTheDocument();
    await view.user.click(within(screen.getByRole('heading', { name: 'Challenge history' }).closest('section')!).getByRole('button', { name: 'Next' }));
    await waitFor(() => expect(screen.getByText('ffc_2')).toBeInTheDocument());
  });
  it.each([false, true])(
    'keeps the exact unlock confirmation and applies one accepted operation (%s)',
    async (accept) => {
      const writes: string[] = [];
      let unlocked = false;
      vi.stubGlobal(
        'fetch',
        vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
          const path = String(input);
          if (init?.method === 'POST') {
            writes.push(path);
            expect(JSON.parse(String(init?.body))).toEqual({ expected_revision: visible.revision });
            unlocked = true;
            return reply({ ...progress, unlocked: true });
          }
          if (path === '/api/session') return reply(session);
          if (path.includes('/challenges/current')) return reply(null);
          if (path.includes('/periods/ffp_one/nodes/ffn_visible'))
            return reply({
              ...visible,
              progress: { ...progress, unlocked },
              eligible: true,
              condition_hint: { kind: 'all', met: true, hidden_count: 0, children: [] },
            });
          if (path.includes('/periods/ffp_one/leaderboard'))
            return reply({
              period_id: 'ffp_one',
              final: false,
              page: 1,
              page_size: 20,
              total: 0,
              rows: [],
            });
          if (path.includes('/periods/ffp_one')) return reply(period);
          if (path.includes('/periods?page='))
            return reply({
              items: [{ ...period, nodes: undefined }],
              page: 1,
              page_size: 20,
              has_more: false,
            });
          if (path.includes('/history'))
            return reply({ items: [], page: 1, page_size: 20, has_more: false });
          throw new Error(`Unexpected request ${path}`);
        }),
      );
      const view = await renderWithProviders(<FatFishActivityPage />, {
        station: 'user',
        role: 'user',
        route: '/activities/fat-fish?period=ffp_one&node=ffn_visible',
      });
      const unlock = await screen.findByRole('button', { name: /Unlock/ });
      await view.user.click(unlock);
      const dialog = screen.getByRole('alertdialog');
      expect(dialog).toHaveTextContent(visible.amounts.unlock_cost);
      expect(writes).toEqual([]);
      if (accept) {
        const confirm = within(dialog).getByRole('button', { name: /Unlock/ });
        await act(async () => {
          confirm.click();
          confirm.click();
        });
        await waitFor(() => expect(writes).toHaveLength(1));
        expect(writes[0]).toContain('/unlock');
        return;
      }
      await view.user.click(within(dialog).getByRole('button', { name: 'Cancel' }));
      expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument();
      expect(writes).toEqual([]);
      await view.user.click(unlock);
      expect(screen.getByRole('alertdialog')).toBeInTheDocument();
      view.unmount();
      await act(async () => undefined);
      expect(writes).toEqual([]);
    },
  );
  it('disposes an in-flight recovery on challenge change and unmount without adopting late results', async () => {
    const challenge: FatFishChallenge = {
      id: 'ffc_first', state: 'active', period_id: 'ffp_one', node_id: 'ffn_visible',
      version_id: 'ffv_one', node_revision: '1', content_hash: 'a'.repeat(64),
      engine_version: 1, scoring_version: 1, seed_commit: 'b'.repeat(64),
      prepared_at_ms: 1000, prepare_until_ms: 61000, start_at_ms: 2000,
      end_at_ms: 12000, submit_until_ms: 1812000, server_now_ms: 3000, ticket_price: '2',
    };
    const completions = new Map<string, (value: FatFishChallenge) => void>();
    const sessions = new Map<string, FatFishSessionController>();
    const recover = vi.spyOn(FatFishSessionController.prototype, 'recover').mockImplementation(function (this: FatFishSessionController, id) {
      sessions.set(id, this);
      return new Promise<FatFishChallenge>((resolve) => { completions.set(id, resolve); });
    });
    const dispose = vi.spyOn(FatFishSessionController.prototype, 'dispose');
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input);
      if (path === '/api/session') return reply(session);
      if (path.includes('/challenges/current')) return reply(challenge);
      if (path.includes('/periods?page=')) return reply({ items: [], page: 1, page_size: 20, has_more: false });
      if (path.includes('/history')) return reply({ items: [], page: 1, page_size: 20, has_more: false });
      throw new Error(`Unexpected request ${path}`);
    }));
    const view = await renderWithProviders(<FatFishActivityPage />, { station: 'user', role: 'user' });
    await waitFor(() => expect(recover).toHaveBeenCalledWith('ffc_first'));
    const replacement = { ...challenge, id: 'ffc_second' };
    act(() => view.queryClient.setQueryData(['user', 'fat-fish', '1', 'current'], replacement));
    await waitFor(() => expect(recover).toHaveBeenCalledWith('ffc_second'));
    expect(dispose.mock.instances).toContain(sessions.get('ffc_first'));
    act(() => completions.get('ffc_first')?.(challenge));
    await waitFor(() => expect(screen.getByText(/This tab cannot continue playing/)).toBeInTheDocument());
    view.unmount();
    expect(dispose.mock.instances).toContain(sessions.get('ffc_second'));
    act(() => completions.get('ffc_second')?.(replacement));
  });
});
