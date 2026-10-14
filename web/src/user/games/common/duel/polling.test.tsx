import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, renderHook } from '@testing-library/react';
import type { ReactNode } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useBlackjack } from '../../blackjack/api';
import { blackjackWire } from '../../blackjack/testFixtures';
import { biddingCodec } from '../../bidding/normalize';
import { biddingHomeWire, biddingID } from '../../bidding/testFixtures';
import { useDuel } from './api';

type State = 'idle' | 'terminal' | 'active' | 'queue';

function blackjackHome(state: State): unknown {
  if (state === 'active') return blackjackWire('decision', null);
  if (state === 'terminal') return { ...blackjackWire('result'), queue_count: '0' };
  const idle = {
    ...blackjackWire('seating'),
    you: null,
    your_seat: null,
    table: null,
    queue_count: '0',
  };
  return state === 'idle'
    ? idle
    : {
        ...idle,
        queue_count: '1',
        you: {
          id: 'bjq_AAAAAAAAAAAAAAAAAAAAAA',
          position: '1',
          state: 'waiting',
          stake: '5',
          rake_bp: { platform: 100, welfare: 100, thursday: 100 },
          payment: { general: '5', game: '0' },
          seat: null,
          session_id: null,
          pending: false,
          legal_actions: { '0': [], '1': [] },
        },
      };
}

function biddingHome(state: State): unknown {
  if (state === 'active') return biddingHomeWire();
  const idle = { server_now: 1_800_000_000, current: null, queue: null, latest_result: null };
  if (state === 'idle') return idle;
  if (state === 'queue')
    return {
      ...idle,
      queue: {
        id: 'bidq_AAAAAAAAAAAAAAAAAAAAAA',
        revision: '1',
        mode: 'tier1',
        deadline: 1_800_000_100,
        ticket: '5',
        payment: { general: '5', game: '0' },
        terms_hash: 'a'.repeat(64),
        rules_version: 1,
      },
    };
  return {
    ...idle,
    latest_result: {
      id: biddingID,
      game: 'bidding',
      mode: 'tier1',
      terminal_at: 1_800_000_000,
      outcome: 'win',
      reason: 'rounds',
      scores: [1, 0],
      own_payment: { general: '5', game: '0' },
      own_refund: { general: '0', game: '0' },
      prize_general: '9.7',
      rake: { platform: '0.1', welfare: '0.1', thursday: '0.1' },
      resolution: null,
      you: 0,
      view: null,
      profiles: [{ kind: 'anonymous' }, { kind: 'anonymous' }],
    },
  };
}

const variants = [
  { name: 'blackjack', useGame: useBlackjack, wire: blackjackHome, activeInterval: 700 },
  {
    name: 'two-player games',
    useGame: () => useDuel(biddingCodec),
    wire: biddingHome,
    activeInterval: 1_000,
  },
] as const;

const clients: QueryClient[] = [];
beforeEach(() => {
  vi.useFakeTimers();
  vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('visible');
});
afterEach(() => {
  for (const client of clients.splice(0)) client.clear();
  vi.useRealTimers();
});

async function advance(ms: number) {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(ms);
  });
}

async function setup(variant: (typeof variants)[number], state: State, failing = false) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  clients.push(client);
  const fetch = vi.fn(
    async () =>
      new Response(JSON.stringify(variant.wire(state)), {
        headers: { 'Content-Type': 'application/json' },
      }),
  );
  if (failing) fetch.mockRejectedValueOnce(new TypeError('Temporary connection failure'));
  vi.stubGlobal('fetch', fetch);
  const view = renderHook(() => variant.useGame(), {
    wrapper: ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    ),
  });
  await advance(1);
  return { view, fetch };
}

for (const variant of variants) {
  describe(`${variant.name} polling`, () => {
    it.each(['idle', 'terminal'] as const)(
      'reads the %s state every five seconds',
      async (state) => {
        const { view, fetch } = await setup(variant, state);
        expect(view.result.current.query.isSuccess).toBe(true);
        expect(fetch).toHaveBeenCalledTimes(1);
        await advance(4_998);
        expect(fetch).toHaveBeenCalledTimes(1);
        await advance(2);
        expect(fetch).toHaveBeenCalledTimes(2);
      },
    );

    it.each(['active', 'queue'] as const)('keeps the %s state responsive', async (state) => {
      const { view, fetch } = await setup(variant, state);
      expect(view.result.current.query.isSuccess).toBe(true);
      await advance(variant.activeInterval);
      expect(fetch).toHaveBeenCalledTimes(2);
    });

    it('stops hidden polling and refreshes immediately when visible again', async () => {
      const { view, fetch } = await setup(variant, 'active');
      const visibility = vi.spyOn(document, 'visibilityState', 'get');
      act(() => {
        visibility.mockReturnValue('hidden');
        document.dispatchEvent(new Event('visibilitychange', { bubbles: true }));
      });
      await advance(10_000);
      expect(fetch).toHaveBeenCalledTimes(1);
      act(() => {
        visibility.mockReturnValue('visible');
        document.dispatchEvent(new Event('visibilitychange', { bubbles: true }));
      });
      await advance(1);
      expect(fetch).toHaveBeenCalledTimes(2);
      expect(view.result.current.query.isSuccess).toBe(true);
    });

    it('recovers from a failed read at five seconds and resumes the active interval', async () => {
      const { view, fetch } = await setup(variant, 'active', true);
      expect(view.result.current.query.isError).toBe(true);
      await advance(4_998);
      expect(fetch).toHaveBeenCalledTimes(1);
      await advance(2);
      expect(view.result.current.query.isSuccess).toBe(true);
      expect(fetch).toHaveBeenCalledTimes(2);
      await advance(variant.activeInterval);
      expect(fetch).toHaveBeenCalledTimes(3);
    });
  });
}
