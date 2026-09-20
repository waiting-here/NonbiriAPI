import { act, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { blackjackState } from '@shared/games/blackjack';
import { blackjackAdminDetail } from '../../../admin/features/games/blackjack';
import { renderWithProviders } from '../../../../test/unit/support';
import { gamesSnapshotWire } from '../common/testFixtures';
import { BlackjackGame } from './BlackjackGame';
import { blackjackKeys } from './api';
import { blackjackWire, tableID } from './testFixtures';

const json = (body: unknown, status = 200) =>
  new Response(status === 204 ? null : JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json' },
  });
function install(home = blackjackWire()) {
  const state = { home, requests: [] as { url: string; init?: RequestInit }[], failAction: false };
  const snapshot = gamesSnapshotWire();
  snapshot.blackjack.enabled = true;
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string, init?: RequestInit) => {
      state.requests.push({ url, init });
      if (url === '/api/games') return json(snapshot);
      if (url === '/api/games/blackjack/state') return json(state.home);
      if (url.endsWith('/actions')) {
        if (state.failAction) {
          state.failAction = false;
          throw new TypeError('lost response');
        }
        const body = JSON.parse(String(init?.body)) as { hand: number; revision: string };
        return json(
          { session_id: tableID, batch_at: state.home.server_now + 1, ...body, action: undefined },
          202,
        );
      }
      if (url.endsWith('/emotes')) return json(null, 204);
      if (url.includes('/history?')) return json({ items: [], next_cursor: null });
      throw new Error(`unexpected test request ${url}`);
    }),
  );
  return state;
}
beforeEach(() => {
  vi.stubGlobal(
    'matchMedia',
    vi.fn(() => ({ matches: false, addEventListener() {}, removeEventListener() {} })),
  );
  Object.defineProperty(HTMLDialogElement.prototype, 'showModal', {
    configurable: true,
    value() {
      this.setAttribute('open', '');
    },
  });
  Object.defineProperty(HTMLDialogElement.prototype, 'close', {
    configurable: true,
    value() {
      this.removeAttribute('open');
    },
  });
});
afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('blackjack public state and simultaneous controls', () => {
  it('renders all nine seats, hides the hole, and submits the exact own hand revision', async () => {
    const server = install();
    const view = await renderWithProviders(<BlackjackGame />, {
      station: 'user',
      route: '/games/blackjack',
      role: 'user',
    });
    expect(await screen.findByRole('region', { name: 'Your hands' })).toBeVisible();
    expect(screen.getAllByRole('region', { name: /^Seat / })).toHaveLength(8);
    expect(screen.getByRole('img', { name: 'Dealer hole card' })).toBeVisible();
    await view.user.click(screen.getByRole('button', { name: 'Hit' }));
    await waitFor(() =>
      expect(server.requests.filter((r) => r.url.endsWith('/actions'))).toHaveLength(1),
    );
    const request = server.requests.find((r) => r.url.endsWith('/actions'))!;
    expect(JSON.parse(String(request.init?.body))).toEqual({
      hand: 0,
      revision: '1',
      action: 'hit',
    });
    expect(new Headers(request.init?.headers).get('Idempotency-Key')).toMatch(
      /^[A-Za-z0-9_-]{32}$/,
    );
  });

  it('blocks a second intent after a lost response and retries with the same key and body', async () => {
    const server = install();
    server.failAction = true;
    const view = await renderWithProviders(<BlackjackGame />, {
      station: 'user',
      route: '/games/blackjack',
      role: 'user',
    });
    await view.user.click(await screen.findByRole('button', { name: 'Hit' }));
    const retry = await screen.findByRole('button', { name: 'Confirm previous action' });
    expect(screen.getByRole('button', { name: 'Stand' })).toBeDisabled();
    await view.user.click(retry);
    await waitFor(() =>
      expect(server.requests.filter((r) => r.url.endsWith('/actions'))).toHaveLength(2),
    );
    const [first, second] = server.requests.filter((r) => r.url.endsWith('/actions'));
    expect(second.init?.body).toBe(first.init?.body);
    expect(new Headers(second.init?.headers).get('Idempotency-Key')).toBe(
      new Headers(first.init?.headers).get('Idempotency-Key'),
    );
  });

  it('keeps both split hands accessible and pending actions block the whole seat', async () => {
    const home = blackjackWire();
    home.table.fact.cards!.seats[0].hands = [0, 1].map((i) => ({
      ...home.table.fact.cards!.seats[0].hands[0],
      split: true,
      revision: String(i + 2),
    }));
    home.you!.legal_actions = { '0': ['hit', 'stand', 'double'], '1': ['hit', 'stand', 'double'] };
    const server = install(home);
    const view = await renderWithProviders(<BlackjackGame />, {
      station: 'user',
      route: '/games/blackjack',
      role: 'user',
    });
    const hits = await screen.findAllByRole('button', { name: 'Hit' });
    expect(hits).toHaveLength(2);
    await view.user.click(hits[1]);
    await waitFor(() => expect(server.requests.some((r) => r.url.endsWith('/actions'))).toBe(true));
    expect(
      JSON.parse(String(server.requests.find((r) => r.url.endsWith('/actions'))?.init?.body)),
    ).toMatchObject({ hand: 1, revision: '3' });
    server.home.you!.pending = true;
    server.home.you!.legal_actions = {};
    await act(async () => {
      await view.queryClient.invalidateQueries({ queryKey: blackjackKeys.state });
    });
    expect(
      await screen.findByText('Action accepted; waiting for this second’s deal.'),
    ).toBeVisible();
    expect(screen.queryByRole('button', { name: 'Hit' })).not.toBeInTheDocument();
  });

  it('presents server payout facts and keeps history queries separate from detail queries', async () => {
    install(blackjackWire('result'));
    const view = await renderWithProviders(<BlackjackGame />, {
      station: 'user',
      route: '/games/blackjack',
      role: 'user',
    });
    const results = await screen.findByRole('region', { name: 'Settlement details' });
    expect(results).toHaveTextContent('9,700');
    expect(results).toHaveTextContent('Platform fee');
    expect(screen.queryByRole('img', { name: 'Dealer hole card' })).not.toBeInTheDocument();
    await view.user.click(screen.getByRole('button', { name: 'History' }));
    expect(await within(screen.getByRole('dialog')).findByText('No games yet.')).toBeVisible();
  });

  it('animates new cards once, skips historical restoration and respects reduced motion', async () => {
    const server = install();
    const animate = vi.fn(() => ({ cancel: vi.fn() }));
    Object.defineProperty(HTMLElement.prototype, 'animate', { configurable: true, value: animate });
    const view = await renderWithProviders(<BlackjackGame />, {
      station: 'user',
      route: '/games/blackjack',
      role: 'user',
    });
    await screen.findByRole('region', { name: 'Your hands' });
    expect(animate).not.toHaveBeenCalled();
    server.home.server_now++;
    server.home.table.fact.cards!.seats[0].hands[0].cards.push({ rank: 2, suit: 1 });
    server.home.table.fact.cards!.seats[0].hands[0].total.value = 18;
    await act(async () => {
      await view.queryClient.invalidateQueries({ queryKey: blackjackKeys.state });
    });
    await waitFor(() => expect(animate).toHaveBeenCalled());
    const count = animate.mock.calls.length;
    await act(async () => {
      await view.queryClient.invalidateQueries({ queryKey: blackjackKeys.state });
    });
    expect(animate).toHaveBeenCalledTimes(count);
    server.home.server_now += 10;
    server.home.table.fact.cards!.seats[0].hands[0].cards.push({ rank: 2, suit: 0 });
    await act(async () => {
      await view.queryClient.invalidateQueries({ queryKey: blackjackKeys.state });
    });
    expect(animate).toHaveBeenCalledTimes(count);
    vi.stubGlobal(
      'matchMedia',
      vi.fn(() => ({ matches: true, addEventListener() {}, removeEventListener() {} })),
    );
    server.home.server_now++;
    server.home.table.fact.cards!.seats[0].hands[0].cards.push({ rank: 1, suit: 0 });
    await act(async () => {
      await view.queryClient.invalidateQueries({ queryKey: blackjackKeys.state });
    });
    expect(animate).toHaveBeenCalledTimes(count);
    delete (HTMLElement.prototype as Partial<HTMLElement>).animate;
  });

  it('offers only public spectator controls and validates configured stake steps', async () => {
    install(blackjackWire('decision', null));
    await renderWithProviders(<BlackjackGame />, {
      station: 'user',
      route: '/games/blackjack',
      role: 'user',
    });
    const stake = await screen.findByRole('spinbutton', { name: 'Base stake' });
    expect(screen.queryByRole('region', { name: 'Hand actions' })).not.toBeInTheDocument();
    expect(screen.queryByRole('group', { name: 'Preset emotes' })).not.toBeInTheDocument();
    fireEvent.change(stake, { target: { value: '1500' } });
    expect(screen.getByRole('button', { name: 'Join queue' })).toBeDisabled();
    fireEvent.change(stake, { target: { value: '2000' } });
    expect(screen.getByRole('button', { name: 'Join queue' })).toBeEnabled();
  });

  it('selects quick amounts without a write and preserves the selection across configuration changes', async () => {
    const server = install(blackjackWire('seating', null));
    const view = await renderWithProviders(<BlackjackGame />, {
      station: 'user',
      route: '/games/blackjack',
      role: 'user',
    });
    const group = await screen.findByRole('group', { name: 'Quick stake selection' });
    const stake = screen.getByRole('spinbutton', { name: 'Base stake' });
    for (const amount of ['1,000', '50,000', '10,000'])
      await view.user.click(within(group).getByRole('button', { name: amount }));
    expect(stake).toHaveValue(10000);
    expect(within(group).getByRole('button', { name: '10,000' })).toHaveAttribute(
      'aria-pressed',
      'true',
    );
    expect(server.requests.filter((r) => r.init?.method && r.init.method !== 'GET')).toHaveLength(
      0,
    );
    server.home.config.default_stake = '50000';
    server.home.config.quick_stakes = ['1000', '50000'];
    await act(async () => {
      await view.queryClient.invalidateQueries({ queryKey: blackjackKeys.state });
    });
    await waitFor(() => expect(within(group).getAllByRole('button')).toHaveLength(2));
    expect(stake).toHaveValue(10000);
    expect(within(group).queryByRole('button', { pressed: true })).not.toBeInTheDocument();
    server.home.config.min_stake = '20000';
    server.home.config.quick_stakes = [];
    await act(async () => {
      await view.queryClient.invalidateQueries({ queryKey: blackjackKeys.state });
    });
    await waitFor(() =>
      expect(
        screen.queryByRole('group', { name: 'Quick stake selection' }),
      ).not.toBeInTheDocument(),
    );
    expect(stake).toHaveValue(10000);
    expect(screen.getByRole('button', { name: 'Join queue' })).toBeDisabled();
  });

  it('decodes a legacy home without inventing quick stakes and rejects malformed present values', () => {
    const home = blackjackWire('seating', null);
    const old: Partial<typeof home.config> = { ...home.config };
    delete old.quick_stakes;
    expect(blackjackState({ ...home, config: old }).config.quick_stakes).toBeUndefined();
    for (const quick_stakes of [null, '1000', ['1000', '1000'], ['10000', '1000'], ['1001']]) {
      expect(() => blackjackState({ ...home, config: { ...home.config, quick_stakes } })).toThrow();
    }
  });

  it('rejects private shoe fields, unexpected seats, and identity-bearing anonymous exports', () => {
    const home = blackjackWire();
    expect(blackjackState(home).table?.fact.cards?.dealer).toHaveLength(1);
    expect(() => blackjackState({ ...home, table: { ...home.table, deck: [] } })).toThrow();
    const extra = structuredClone(home);
    extra.table.fact.cards!.seats.push({ ...extra.table.fact.cards!.seats[0], number: 8 });
    expect(() => blackjackState(extra)).toThrow();
    expect(() =>
      blackjackAdminDetail({
        id: 'bja_AAAAAAAAAAAAAAAAAAAAAA',
        dataset: 'anonymous',
        record: {
          rules_version: 1,
          phase: 'result',
          reason: 'completed',
          fact: blackjackWire('result').table.fact,
        },
        recent: { user_id: '1' },
      }),
    ).toThrow();
  });
});
