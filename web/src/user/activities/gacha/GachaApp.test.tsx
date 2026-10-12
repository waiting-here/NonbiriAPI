import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import { GachaApp } from './GachaApp';
import { Shop } from './Shop';
import { themes } from './catalog';
import { defaultRules, consolidatedRates } from './rules';
import type { GachaPort, Outcome, Snapshot } from './types';

function snapshot(): Snapshot {
  return {
    visible_themes: themes.map((theme) => theme.id),
    tickets: { permanent: 10, limited: 0 },
    fragments: { N: 0, R: 0, SR: 0, SSR: 0 },
    credits: { general: '10000', game: '10000' },
    owned: {},
    pity: {},
    newcomer: true,
    checked_in: false,
    limited_checked_in: false,
    claimed: [],
    redeemed: [],
    month_used: { permanent: 0, limited: 0 },
    donation: '0',
    server_time: 1800000000,
    time_zone: 'UTC+08:00',
    pools: [
      {
        id: 'permanent',
        name: '永续智能',
        cover: 'permanent',
        kind: 'permanent',
        group: 'permanent',
        rules: defaultRules,
        featured: [],
        starts_at: null,
        ends_at: null,
        paused: false,
        published: true,
        revision: 1,
        rates: consolidatedRates(defaultRules),
      },
    ],
    shop: {
      permanent_price: '10000',
      limited_price: '10000',
      n_ticket: 20,
      r_ticket: 10,
      sr_ticket: 5,
      ssr_tickets: 1,
      permanent_cap: 20,
      limited_cap: 10,
      sr_card: 10,
      ssr_card: 3,
      sp_card: 9,
      chibi_n: 30,
      chibi_r: 15,
      revision: 1,
    },
  };
}

it.each([false, true])(
  'labels every shop card with its actual shard cost, English=%s',
  (english) => {
    const state = snapshot();
    state.pools.push({
      ...state.pools[0],
      id: 'limited',
      kind: 'limited',
      starts_at: state.server_time - 60,
      ends_at: state.server_time + 60,
      featured: ['h-chatgpt', 'j-chatgpt', 'l-chatgpt'],
    });
    const view = render(
      <Shop state={state} english={english} busy={false} act={vi.fn()} inspect={vi.fn()} />,
    );
    const prices = [...view.container.querySelectorAll('.gacha-shop-card__price')].map(
      (node) => node.textContent,
    );
    if (english) {
      expect(prices).toContain('Cost: 30 N shards and 15 R shards');
      expect(prices).toContain('Cost: 10 SR shards');
      expect(prices).toContain('Cost: 3 SSR shards');
      expect(prices).toContain('Cost: 9 SSR shards');
    } else {
      expect(prices).toContain('兑换需要：30 N 碎片 和 15 R 碎片');
      expect(prices).toContain('兑换需要：10 SR 碎片');
      expect(prices).toContain('兑换需要：3 SSR 碎片');
      expect(prices).toContain('兑换需要：9 SSR 碎片');
    }
  },
);
it('claims both daily tickets independently', async () => {
  const state = snapshot();
  const port: GachaPort = {
    scope: 'user:both-daily-tickets',
    load: async () => structuredClone(state),
    history: async () => ({ items: [] }),
    act: async (input) => {
      if (input.action === 'check-in') {
        state.checked_in = true;
        state.tickets.permanent++;
      }
      if (input.action === 'limited-check-in') {
        state.limited_checked_in = true;
        state.tickets.limited++;
      }
      return { snapshot: structuredClone(state) };
    },
  };
  render(<GachaApp port={port} />);
  fireEvent.click(await screen.findByRole('button', { name: '签到 · 常驻券 +1' }));
  expect(await screen.findByRole('button', { name: '常驻券已领取' })).toBeDisabled();
  fireEvent.click(screen.getByRole('button', { name: '签到 · 限定券 +1' }));
  expect(await screen.findByRole('button', { name: '限定券已领取' })).toBeDisabled();
  expect(state.tickets).toEqual({ permanent: 11, limited: 1 });
});

beforeEach(() => {
  localStorage.setItem(
    'nb.gacha.preferences',
    JSON.stringify({ music: false, sound: false, reduced: true }),
  );
});

it('retries a lost draw response with the original operation after remounting', async () => {
  const state = snapshot(),
    keys: string[] = [];
  const receipt = {
    id: 'receipt',
    pool_id: 'permanent',
    pool_name: '永续智能',
    created_at: state.server_time,
    items: [{ card_id: 'g-chatgpt', duplicate: false, guarantee: 'newcomer' as const }],
  };
  let committed = false;
  const port: GachaPort = {
    scope: 'user:retry-player',
    load: vi.fn(async () => structuredClone(state)),
    history: async () => ({ items: [] }),
    act: vi.fn(async (input, key) => {
      if (input.action === 'draw') {
        keys.push(key);
        if (!committed) {
          committed = true;
          state.tickets.permanent -= 10;
          throw new Error('response lost after commit');
        }
        return { snapshot: structuredClone(state), draw: receipt };
      }
      return { snapshot: structuredClone(state) };
    }),
  };
  const first = render(<GachaApp port={port} />);
  fireEvent.click(await screen.findByRole('button', { name: /十次召唤/ }));
  expect(await screen.findByRole('button', { name: '重试原操作' })).toBeEnabled();
  expect(state.tickets.permanent).toBe(0);
  first.unmount();
  render(<GachaApp port={port} />);
  fireEvent.click(await screen.findByRole('button', { name: '重试原操作' }));
  expect(await screen.findByRole('dialog', { name: '此次相遇' })).toBeVisible();
  expect(keys).toHaveLength(2);
  expect(keys[0]).toBe(keys[1]);
  expect(state.tickets.permanent).toBe(0);
  expect(sessionStorage.getItem('nb.gacha.pending:' + port.scope)).toBeNull();
});

it('does not reveal a late result or reuse pending work for a different account', async () => {
  const state = snapshot();
  let complete!: (value: Outcome) => void;
  const oldPort: GachaPort = {
    scope: 'user:first',
    load: async () => state,
    history: async () => ({ items: [] }),
    act: vi.fn(async (input) =>
      input.action === 'draw'
        ? new Promise<Outcome>((resolve) => {
            complete = resolve;
          })
        : { snapshot: state },
    ),
  };
  const first = render(<GachaApp port={oldPort} />);
  fireEvent.click(await screen.findByRole('button', { name: /十次召唤/ }));
  await waitFor(() => expect(complete).toBeTypeOf('function'));
  first.unmount();
  const next: GachaPort = {
    scope: 'user:second',
    load: async () => state,
    history: async () => ({ items: [] }),
    act: vi.fn(async () => ({ snapshot: state })),
  };
  render(<GachaApp port={next} />);
  await screen.findByRole('button', { name: /十次召唤/ });
  complete({
    snapshot: {
      ...state,
      visible_themes: themes.map((theme) => theme.id),
      tickets: { permanent: 0, limited: 0 },
    },
  });
  await waitFor(() => expect(next.act).toHaveBeenCalledTimes(1));
  expect(vi.mocked(next.act).mock.calls[0][0]).toEqual({ action: 'enter' });
  expect(screen.queryByRole('button', { name: '重试原操作' })).not.toBeInTheDocument();
  expect(screen.getByRole('button', { name: /十次召唤/ })).toBeEnabled();
});

it.each([false, true])(
  'offers repeat draws only with tickets and an open original pool (paused=%s)',
  async (paused) => {
    const state = snapshot(),
      keys: string[] = [];
    state.tickets.permanent = 20;
    const port: GachaPort = {
      scope: 'user:repeat-' + paused,
      load: async () => structuredClone(state),
      history: async () => ({ items: [] }),
      act: vi.fn(async (input, key) => {
        if (input.action !== 'draw') return { snapshot: structuredClone(state) };
        keys.push(key);
        expect(input).toEqual({ action: 'draw', pool_id: 'permanent', quantity: 10 });
        state.tickets.permanent -= 10;
        state.pools[0].paused = paused;
        return {
          snapshot: structuredClone(state),
          draw: {
            id: key,
            pool_id: 'permanent',
            pool_name: '永续智能',
            created_at: state.server_time,
            items: Array.from({ length: 10 }, () => ({
              card_id: 'a-chatgpt',
              duplicate: true,
              guarantee: 'none' as const,
            })),
          },
        };
      }),
    };
    render(<GachaApp port={port} />);
    fireEvent.click(await screen.findByRole('button', { name: /十次召唤/ }));
    await screen.findByRole('dialog', { name: '此次相遇' });
    if (paused) {
      expect(screen.queryByRole('button', { name: '再来一次' })).not.toBeInTheDocument();
      expect(keys).toHaveLength(1);
    } else {
      fireEvent.click(screen.getByRole('button', { name: '再来一次' }));
      await waitFor(() => expect(keys).toHaveLength(2));
      await screen.findByRole('dialog', { name: '此次相遇' });
      expect(keys[1]).not.toBe(keys[0]);
      expect(state.tickets.permanent).toBe(0);
      expect(screen.queryByRole('button', { name: '再来一次' })).not.toBeInTheDocument();
    }
  },
);
