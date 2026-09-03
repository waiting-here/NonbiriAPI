import { act, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { installJsonFetchFixtures, renderWithProviders } from '../../../test/unit/support';
import { userKeys } from '../data';
import { GameCenter } from './GameCenter';
import { gameKeys } from './common/snapshot';
import { gamesSnapshotWire } from './common/testFixtures';
import { FishingGame } from './fishing/FishingGame';
import { LinkLinkGame } from './linklink/LinkLinkGame';
import { RPSGame } from './rps/RPSGame';
import { rpsKeys } from './rps/api';
import { normalizeRPSHome } from './rps/normalize';
import { rpsStateWire, rpsTestSessionID } from './rps/testFixtures';

function emptyFishingBoard(board: 'single' | 'total') {
  return { board, window_start: board === 'single' ? null : 1_700_000_000, entries: [], me: null };
}
function fishingResult() {
  return {
    batch_id: 'fb_AAAAAAAAAAAAAAAAAAAAAA',
    bait: 'worm',
    count: 1,
    unit_price: '1',
    entry_total: '1',
    outcomes: [{ ordinal: 0, species_key: 'whitebait', tier: 'small', size_cm: 12, reward: '2' }],
    payout_total: '2',
    balance: '12345678901234567891.125',
    settled_at: 1_800_000_000,
    idempotent_replay: false,
  };
}
function linkLinkState() {
  return {
    session_id: 'll_AAAAAAAAAAAAAAAAAAAAAA',
    spec: '6x8',
    price: '3',
    state: 'active',
    revision: '1',
    board: {
      rows: 6,
      cols: 8,
      tiles: Array.from({ length: 48 }, (_, index) => ({
        row: Math.floor(index / 8),
        col: index % 8,
        tile_key: `tile_${String(Math.floor(index / 4) + 1).padStart(2, '0')}`,
        removed: false,
      })),
    },
    pairs_removed: 0,
    total_pairs: 24,
    started_at: 1_800_000_000,
    deadline: 1_800_000_150,
    server_now: 1_800_000_010,
  };
}
function emptyRPSBoard(mode: string, board: 'profit_rate' | 'net_profit') {
  return {
    mode,
    board,
    window_days: 30,
    window_start: 1_700_000_000,
    min_sessions: 10,
    rows: [],
    me: null,
  };
}
function rpsPendingResult(mode = 'standard') {
  return {
    kind: 'pending_result',
    result: {
      session_id: rpsTestSessionID,
      mode,
      terminal_reason: mode === 'quick' ? 'quick_resolved' : 'standard_round_limit',
      own_seat_no: 0,
      own_input: '1',
      own_returned: '2',
      own_wallet_net: '1',
      seats: [
        { seat_no: 0, result: 'win' },
        { seat_no: 1, result: 'loss' },
        { seat_no: 2, result: 'deidentified' },
      ],
      created_at: 1_800_000_000,
    },
  };
}

describe('beta.1 game pages', () => {
  afterEach(() => vi.unstubAllGlobals());

  it('renders three center cards with open, partial, and explicitly closed availability', async () => {
    const snapshot = gamesSnapshotWire();
    snapshot.fishing.enabled = false;
    snapshot.rps.modes.standard.enabled = false;
    installJsonFetchFixtures([{ method: 'GET', path: '/api/games', body: snapshot }]);
    await renderWithProviders(<GameCenter />, { station: 'user', route: '/games', role: 'user' });
    expect(await screen.findByRole('heading', { name: 'Choose your pace' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Pond fishing' })).toBeInTheDocument();
    expect(screen.getAllByText('Partly open')).toHaveLength(2);
    expect(screen.getByText('Closed')).toBeInTheDocument();
    expect(screen.getAllByRole('link')).toHaveLength(3);
  });

  it('shows maintenance as a state and exposes no game entry links', async () => {
    installJsonFetchFixtures([
      {
        method: 'GET',
        path: '/api/games',
        status: 503,
        body: { error: { code: 'maintenance', message: 'maintenance' } },
      },
    ]);
    await renderWithProviders(<GameCenter />, { station: 'user', route: '/games', role: 'user' });
    expect(await screen.findAllByText('Maintenance')).toHaveLength(6);
    expect(screen.queryByRole('link')).not.toBeInTheDocument();
  });

  it('keeps each game’s rules entry available during maintenance', async () => {
    for (const [element, route] of [
      [<FishingGame />, '/games/fishing'],
      [<LinkLinkGame />, '/games/linklink'],
      [<RPSGame />, '/games/rps'],
    ] as const) {
      installJsonFetchFixtures([
        {
          method: 'GET',
          path: '/api/games',
          status: 503,
          body: { error: { code: 'maintenance', message: 'maintenance' } },
        },
      ]);
      const rendered = await renderWithProviders(element, {
        station: 'user',
        route,
        role: 'user',
      });
      await rendered.user.click(await screen.findByRole('button', { name: 'How to play' }));
      const dialog = screen.getByRole('dialog');
      await rendered.user.click(within(dialog).getByRole('button', { name: 'Close rules' }));
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
      rendered.unmount();
    }
  });

  it('keeps each game’s rules entry available while the snapshot is loading', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() => new Promise<Response>(() => undefined)),
    );
    for (const [element, route] of [
      [<FishingGame />, '/games/fishing'],
      [<LinkLinkGame />, '/games/linklink'],
      [<RPSGame />, '/games/rps'],
    ] as const) {
      const rendered = await renderWithProviders(element, {
        station: 'user',
        route,
        role: 'user',
      });
      await rendered.user.click(screen.getByRole('button', { name: 'How to play' }));
      const dialog = screen.getByRole('dialog');
      await rendered.user.click(within(dialog).getByRole('button', { name: 'Close rules' }));
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
      rendered.unmount();
    }
  });

  it('keeps each game’s rules openable and closable after a snapshot error', async () => {
    for (const [element, route] of [
      [<FishingGame />, '/games/fishing'],
      [<LinkLinkGame />, '/games/linklink'],
      [<RPSGame />, '/games/rps'],
    ] as const) {
      installJsonFetchFixtures([
        {
          method: 'GET',
          path: '/api/games',
          status: 500,
          body: { error: { code: 'internal', message: 'synthetic failure' } },
        },
      ]);
      const rendered = await renderWithProviders(element, {
        station: 'user',
        route,
        role: 'user',
      });
      await rendered.user.click(await screen.findByRole('button', { name: 'How to play' }));
      const dialog = screen.getByRole('dialog');
      await rendered.user.click(within(dialog).getByRole('button', { name: 'Close rules' }));
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
      rendered.unmount();
    }
  });

  it('renders Fishing single/atomic-ten controls and exact large authoritative money', async () => {
    installJsonFetchFixtures([
      { method: 'GET', path: '/api/games', body: gamesSnapshotWire() },
      {
        method: 'GET',
        path: '/api/games/fishing/state',
        body: { settlement_pending: null, unrevealed: null, has_more_unrevealed: false },
      },
      {
        method: 'GET',
        path: '/api/games/fishing/leaderboard?board=single',
        body: emptyFishingBoard('single'),
      },
      {
        method: 'GET',
        path: '/api/games/fishing/leaderboard?board=total',
        body: emptyFishingBoard('total'),
      },
    ]);
    const rendered = await renderWithProviders(<FishingGame />, {
      station: 'user',
      route: '/games/fishing',
      role: 'level5',
    });
    expect(await screen.findByRole('button', { name: /Ten-catch batch/i })).toBeInTheDocument();
    expect(
      screen.getByRole('heading', { name: 'A quiet cast, a surprise catch' }),
    ).toBeInTheDocument();
    act(() => {
      rendered.queryClient.setQueryData(userKeys.session, { user: { effective_level: 5 } });
    });
    expect(screen.getByRole('button', { name: /Ten-catch batch/i })).toBeInTheDocument();
    await waitFor(() =>
      expect(document.querySelector('.fishing-stage')).toHaveClass('fishing-stage--l4'),
    );
    expect(screen.getAllByText(/12,345,678,901,234,567,890\.125/).length).toBeGreaterThan(0);
    await rendered.user.click(screen.getByRole('button', { name: /Ten-catch batch/i }));
    expect(screen.getByText('Batch total').parentElement).toHaveTextContent('10 credits');
    await rendered.user.click(screen.getByRole('button', { name: 'How to play' }));
    const rules = screen.getByRole('dialog', { name: 'How pond fishing works' });
    expect(rules).toHaveTextContent('Choose your bait');
    await rendered.user.click(within(rules).getByRole('button', { name: 'Close rules' }));
    expect(
      screen.queryByRole('dialog', { name: 'How pond fishing works' }),
    ).not.toBeInTheDocument();
  });

  it('locks Fishing after an unknown start response and replays the exact operation identity', async () => {
    const fetchMock = installJsonFetchFixtures([
      { method: 'GET', path: '/api/games', body: gamesSnapshotWire() },
      {
        method: 'GET',
        path: '/api/games/fishing/state',
        body: { settlement_pending: null, unrevealed: null, has_more_unrevealed: false },
      },
      {
        method: 'GET',
        path: '/api/games/fishing/leaderboard?board=single',
        body: emptyFishingBoard('single'),
      },
      {
        method: 'GET',
        path: '/api/games/fishing/leaderboard?board=total',
        body: emptyFishingBoard('total'),
      },
    ]);
    const fixtures = fetchMock.getMockImplementation()!;
    fetchMock.mockImplementation(async (input, init) => {
      const target = new URL(String(input), window.location.origin);
      if (target.pathname === '/api/games/fishing/batches' && init?.method === 'POST')
        throw new TypeError('synthetic connection loss');
      return fixtures(input, init);
    });
    const rendered = await renderWithProviders(<FishingGame />, {
      station: 'user',
      route: '/games/fishing',
      role: 'user',
    });
    const start = await screen.findByRole('button', { name: 'Start this batch' });
    await rendered.user.click(start);
    expect(await screen.findByText(/result could not be confirmed/i)).toBeInTheDocument();
    expect(start).toBeDisabled();
    await rendered.user.click(screen.getByRole('button', { name: /retry the same action/i }));
    await waitFor(() => {
      const calls = fetchMock.mock.calls.filter(
        ([input, init]) =>
          new URL(String(input), window.location.origin).pathname ===
            '/api/games/fishing/batches' && init?.method === 'POST',
      );
      expect(calls).toHaveLength(2);
      expect((calls[0][1]?.headers as Headers).get('Idempotency-Key')).toBe(
        (calls[1][1]?.headers as Headers).get('Idempotency-Key'),
      );
      expect(calls[0][1]?.body).toBe(calls[1][1]?.body);
    });
  });

  it('renders the complete Fishing result before ACK and localizes its catch', async () => {
    vi.stubGlobal('matchMedia', (query: string) => ({
      matches: query === '(prefers-reduced-motion: reduce)',
      media: query,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    }));
    vi.stubGlobal('requestAnimationFrame', (callback: FrameRequestCallback) =>
      window.setTimeout(() => callback(performance.now()), 0),
    );
    vi.stubGlobal('cancelAnimationFrame', (handle: number) => window.clearTimeout(handle));
    const fetchMock = installJsonFetchFixtures([
      { method: 'GET', path: '/api/games', body: gamesSnapshotWire() },
      {
        method: 'GET',
        path: '/api/games/fishing/state',
        body: {
          settlement_pending: null,
          unrevealed: fishingResult(),
          has_more_unrevealed: false,
        },
      },
      {
        method: 'GET',
        path: '/api/games/fishing/leaderboard?board=single',
        body: emptyFishingBoard('single'),
      },
      {
        method: 'GET',
        path: '/api/games/fishing/leaderboard?board=total',
        body: emptyFishingBoard('total'),
      },
      {
        method: 'POST',
        path: '/api/games/fishing/batches/fb_AAAAAAAAAAAAAAAAAAAAAA/ack',
        status: 204,
        body: undefined,
      },
    ]);
    const rendered = await renderWithProviders(<FishingGame />, {
      station: 'user',
      route: '/games/fishing',
      locale: 'zh',
      role: 'user',
    });
    await rendered.user.click(await screen.findByRole('button', { name: '玩法说明' }));
    const rules = screen.getByRole('dialog', { name: '池塘垂钓怎么玩' });
    expect(rules).toHaveTextContent('先选鱼饵');
    await rendered.user.click(within(rules).getByRole('button', { name: '关闭玩法说明' }));
    expect(screen.queryByRole('dialog', { name: '池塘垂钓怎么玩' })).not.toBeInTheDocument();
    expect(await screen.findAllByText('银鱼')).toHaveLength(2);
    expect(screen.getByText('12 厘米')).toBeInTheDocument();
    await waitFor(() =>
      expect(
        fetchMock.mock.calls.some(
          ([input, init]) =>
            String(input).endsWith('/api/games/fishing/batches/fb_AAAAAAAAAAAAAAAAAAAAAA/ack') &&
            init?.method === 'POST',
        ),
      ).toBe(true),
    );
  });

  it('keeps the Fishing result visible after an ACK failure and removes it after a safe retry', async () => {
    vi.stubGlobal('matchMedia', (query: string) => ({
      matches: query === '(prefers-reduced-motion: reduce)',
      media: query,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    }));
    vi.stubGlobal('requestAnimationFrame', (callback: FrameRequestCallback) =>
      window.setTimeout(() => callback(performance.now()), 0),
    );
    vi.stubGlobal('cancelAnimationFrame', (handle: number) => window.clearTimeout(handle));
    const fetchMock = installJsonFetchFixtures([
      { method: 'GET', path: '/api/games', body: gamesSnapshotWire() },
      {
        method: 'GET',
        path: '/api/games/fishing/state',
        body: {
          settlement_pending: null,
          unrevealed: fishingResult(),
          has_more_unrevealed: false,
        },
      },
      {
        method: 'GET',
        path: '/api/games/fishing/leaderboard?board=single',
        body: emptyFishingBoard('single'),
      },
      {
        method: 'GET',
        path: '/api/games/fishing/leaderboard?board=total',
        body: emptyFishingBoard('total'),
      },
    ]);
    const fixtures = fetchMock.getMockImplementation()!;
    let attempts = 0;
    let acknowledged = false;
    fetchMock.mockImplementation(async (input, init) => {
      const target = new URL(String(input), window.location.origin);
      if (
        target.pathname === '/api/games/fishing/batches/fb_AAAAAAAAAAAAAAAAAAAAAA/ack' &&
        init?.method === 'POST'
      ) {
        attempts += 1;
        if (attempts === 1) {
          return new Response(
            JSON.stringify({ error: { code: 'service_unavailable', message: 'retry later' } }),
            { status: 503, headers: { 'content-type': 'application/json' } },
          );
        }
        acknowledged = true;
        return new Response(undefined, { status: 204 });
      }
      if (target.pathname === '/api/games/fishing/state' && acknowledged) {
        return new Response(
          JSON.stringify({
            settlement_pending: null,
            unrevealed: null,
            has_more_unrevealed: false,
          }),
          { status: 200, headers: { 'content-type': 'application/json' } },
        );
      }
      return fixtures(input, init);
    });
    const rendered = await renderWithProviders(<FishingGame />, {
      station: 'user',
      route: '/games/fishing',
      role: 'user',
    });
    expect(await screen.findAllByText('Whitebait')).toHaveLength(2);
    const retry = await screen.findByRole('button', { name: 'Retry marking as viewed' });
    expect(screen.getByText(/result could not be marked viewed/i)).toBeInTheDocument();
    expect(screen.getAllByText('Whitebait')).toHaveLength(2);
    expect(screen.getByRole('button', { name: 'Start this batch' })).toBeEnabled();
    await rendered.user.click(retry);
    await waitFor(() => expect(screen.queryAllByText('Whitebait')).toHaveLength(0));
    expect(attempts).toBe(2);
  });

  it('replays an unknown Fishing recovery with the same batch and idempotency key', async () => {
    const pending = {
      batch_id: 'fb_AAAAAAAAAAAAAAAAAAAAAA',
      bait: 'worm',
      count: 1,
      entry_total: '1',
      state: 'recovery_required',
      next_attempt_at: null,
      retry_exhausted: true,
    };
    const fetchMock = installJsonFetchFixtures([
      { method: 'GET', path: '/api/games', body: gamesSnapshotWire() },
      {
        method: 'GET',
        path: '/api/games/fishing/state',
        body: { settlement_pending: pending, unrevealed: null, has_more_unrevealed: false },
      },
      {
        method: 'GET',
        path: '/api/games/fishing/leaderboard?board=single',
        body: emptyFishingBoard('single'),
      },
      {
        method: 'GET',
        path: '/api/games/fishing/leaderboard?board=total',
        body: emptyFishingBoard('total'),
      },
    ]);
    const fixtures = fetchMock.getMockImplementation()!;
    let recoverAttempts = 0;
    let recovered = false;
    fetchMock.mockImplementation(async (input, init) => {
      const target = new URL(String(input), window.location.origin);
      if (
        target.pathname === '/api/games/fishing/batches/fb_AAAAAAAAAAAAAAAAAAAAAA/recover' &&
        init?.method === 'POST'
      ) {
        recoverAttempts += 1;
        if (recoverAttempts === 1) throw new TypeError('synthetic connection loss');
        recovered = true;
        return new Response(JSON.stringify(fishingResult()), {
          status: 200,
          headers: { 'content-type': 'application/json' },
        });
      }
      if (target.pathname === '/api/games/fishing/state' && recovered) {
        return new Response(
          JSON.stringify({
            settlement_pending: null,
            unrevealed: fishingResult(),
            has_more_unrevealed: false,
          }),
          { status: 200, headers: { 'content-type': 'application/json' } },
        );
      }
      return fixtures(input, init);
    });
    const rendered = await renderWithProviders(<FishingGame />, {
      station: 'user',
      route: '/games/fishing',
      role: 'user',
    });
    await rendered.user.click(
      await screen.findByRole('button', { name: 'Recover this same batch' }),
    );
    expect(await screen.findByText(/result could not be confirmed/i)).toBeInTheDocument();
    const replay = screen
      .getAllByRole('button', { name: 'Recover this same batch' })
      .find((button) => !(button as HTMLButtonElement).disabled);
    expect(replay).toBeDefined();
    await rendered.user.click(replay!);
    expect(await screen.findAllByText('Whitebait')).toHaveLength(2);
    const calls = fetchMock.mock.calls.filter(
      ([input, init]) =>
        new URL(String(input), window.location.origin).pathname ===
          '/api/games/fishing/batches/fb_AAAAAAAAAAAAAAAAAAAAAA/recover' && init?.method === 'POST',
    );
    expect(calls).toHaveLength(2);
    expect((calls[0][1]?.headers as Headers).get('Idempotency-Key')).toBe(
      (calls[1][1]?.headers as Headers).get('Idempotency-Key'),
    );
  });

  it('renders the recoverable LinkLink board and moves its roving focus with arrows', async () => {
    installJsonFetchFixtures([
      { method: 'GET', path: '/api/games', body: gamesSnapshotWire() },
      { method: 'GET', path: '/api/games/linklink/session', body: linkLinkState() },
      {
        method: 'POST',
        path: '/api/games/linklink/sessions/ll_AAAAAAAAAAAAAAAAAAAAAA/lease',
        body: { expires_at: 1_800_000_025 },
      },
    ]);
    const rendered = await renderWithProviders(<LinkLinkGame />, {
      station: 'user',
      route: '/games/linklink',
      role: 'user',
    });
    const cells = await screen.findAllByRole('gridcell');
    expect(cells).toHaveLength(48);
    expect(cells[0]).toHaveTextContent('1');
    await waitFor(() => expect(cells[0]).toBeEnabled());
    await rendered.user.click(cells[0]);
    expect(screen.getByText(/game will check whether they can connect/i)).toBeInTheDocument();
    await rendered.user.click(screen.getByRole('button', { name: 'How to play' }));
    const rules = screen.getByRole('dialog', { name: 'How LinkLink works' });
    expect(rules).toHaveTextContent('Clear every pair');
    await rendered.user.click(within(rules).getByRole('button', { name: 'Close rules' }));
    expect(screen.queryByRole('dialog', { name: 'How LinkLink works' })).not.toBeInTheDocument();
    cells[0].focus();
    fireEvent.keyDown(cells[0], { key: 'ArrowRight' });
    expect(cells[1]).toHaveFocus();
  });

  it('requires a frozen-cost LinkLink confirmation and submits one paid start intent', async () => {
    const fetchMock = installJsonFetchFixtures([
      { method: 'GET', path: '/api/games', body: gamesSnapshotWire() },
      { method: 'GET', path: '/api/games/linklink/session', body: null },
      {
        method: 'POST',
        path: '/api/games/linklink/sessions',
        status: 201,
        body: linkLinkState(),
      },
      {
        method: 'POST',
        path: '/api/games/linklink/sessions/ll_AAAAAAAAAAAAAAAAAAAAAA/lease',
        body: { expires_at: 1_800_000_025 },
      },
    ]);
    const rendered = await renderWithProviders(<LinkLinkGame />, {
      station: 'user',
      route: '/games/linklink',
      role: 'user',
    });
    expect(
      await screen.findByText(/No active board or recent 30-day summary/i),
    ).toBeInTheDocument();
    await rendered.user.click(screen.getByRole('button', { name: 'Start 6x8' }));
    const dialog = screen.getByRole('dialog', { name: 'Review paid start' });
    expect(dialog).toHaveTextContent('3 credits');
    await rendered.user.click(within(dialog).getByRole('button', { name: 'Start 6x8' }));
    expect(await screen.findAllByRole('gridcell')).toHaveLength(48);
    const startCalls = fetchMock.mock.calls.filter(
      ([input, init]) =>
        new URL(String(input), window.location.origin).pathname ===
          '/api/games/linklink/sessions' && init?.method === 'POST',
    );
    expect(startCalls).toHaveLength(1);
    expect(startCalls[0][1]?.body).toBe(JSON.stringify({ spec: '6x8' }));
    expect((startCalls[0][1]?.headers as Headers).get('Idempotency-Key')).toMatch(
      /^[A-Za-z0-9_-]{32}$/,
    );
  });

  it('renders the latest minimal LinkLink terminal summary with exact timing facts', async () => {
    installJsonFetchFixtures([
      { method: 'GET', path: '/api/games', body: gamesSnapshotWire() },
      {
        method: 'GET',
        path: '/api/games/linklink/session',
        body: {
          session_id: 'll_AAAAAAAAAAAAAAAAAAAAAA',
          spec: '6x8',
          price: '3',
          terminal_reason: 'completed',
          started_at: 1_800_000_000,
          deadline: 1_800_000_150,
          terminal_at: 1_800_000_140,
          pairs_removed: 24,
          total_pairs: 24,
          score: '2410',
        },
      },
    ]);
    const rendered = await renderWithProviders(<LinkLinkGame />, {
      station: 'user',
      route: '/games/linklink',
      locale: 'zh',
      role: 'user',
    });
    expect(await screen.findByText('棋盘已完成')).toBeInTheDocument();
    expect(screen.getByText('本局用时').parentElement).toHaveTextContent('140 秒');
    expect(screen.getByText('结束时剩余时间').parentElement).toHaveTextContent('10 秒');
    expect(screen.getByText('表现分').parentElement).toHaveTextContent('2,410');
    await rendered.user.click(screen.getByRole('button', { name: '玩法说明' }));
    const rules = screen.getByRole('dialog', { name: '连连看怎么玩' });
    expect(rules).toHaveTextContent('消除所有配对');
    await rendered.user.click(within(rules).getByRole('button', { name: '关闭玩法说明' }));
    expect(screen.queryByRole('dialog', { name: '连连看怎么玩' })).not.toBeInTheDocument();
  });

  it('renders three viewer-aware RPS seats and only the current gesture action', async () => {
    const snapshot = gamesSnapshotWire();
    snapshot.tutorial_rps_seen = true;
    const state = rpsStateWire();
    state.seats[0].fun_snapshot = {
      state: 'full',
      completed_count: '10',
      profitable_count: '6',
      rock_count: '4',
      scissors_count: '3',
      paper_count: '3',
    };
    state.seats[1].display_name =
      'A very long opponent name that must wrap safely without becoming authority';
    state.seats[1].timeout_count = '2';
    state.economy.welfare_carry = '0.125';
    installJsonFetchFixtures([
      { method: 'GET', path: '/api/games', body: snapshot },
      { method: 'GET', path: '/api/games/rps/state', body: { kind: 'session', session: state } },
      {
        method: 'POST',
        path: `/api/games/rps/sessions/${rpsTestSessionID}/lease`,
        body: { expires_at: 1_800_000_015 },
      },
      {
        method: 'GET',
        path: '/api/games/rps/leaderboard?mode=quick&board=profit_rate',
        body: emptyRPSBoard('quick', 'profit_rate'),
      },
      {
        method: 'GET',
        path: '/api/games/rps/leaderboard?mode=quick&board=net_profit',
        body: emptyRPSBoard('quick', 'net_profit'),
      },
      {
        method: 'GET',
        path: '/api/games/rps/leaderboard?mode=standard&board=profit_rate',
        body: emptyRPSBoard('standard', 'profit_rate'),
      },
      {
        method: 'GET',
        path: '/api/games/rps/leaderboard?mode=standard&board=net_profit',
        body: emptyRPSBoard('standard', 'net_profit'),
      },
    ]);
    const rendered = await renderWithProviders(<RPSGame />, {
      station: 'user',
      route: '/games/rps',
      role: 'user',
    });
    expect(await screen.findByText('Live match')).toBeInTheDocument();
    expect(screen.getAllByText('Hidden until reveal')).toHaveLength(3);
    expect(screen.getByText(/A very long opponent name/)).toBeInTheDocument();
    expect(screen.getByText('Rock 4 · 40%')).toBeInTheDocument();
    expect(
      screen.getByText(/choices made automatically after a timeout/i),
    ).toBeInTheDocument();
    expect(screen.getByText('Automatic actions: 2')).toBeInTheDocument();
    expect(screen.getByText('Current base stake').parentElement).toHaveTextContent('1 credits');
    expect(screen.getByText('Completed base rounds').parentElement).toHaveTextContent('1');
    expect(screen.getByText('Paid-pool ties').parentElement).toHaveTextContent('0');
    expect(screen.getByText('Free-pool ties (limit 6)').parentElement).toHaveTextContent('0');
    expect(screen.getByText('Current pool streaks').parentElement).toHaveTextContent(
      'paid 0 · free 0',
    );
    expect(screen.getByText('Welfare carry: 0.125 credits')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Rock' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Raise' })).not.toBeInTheDocument();
    await rendered.user.click(screen.getByRole('button', { name: 'How to play' }));
    const rules = screen.getByRole('dialog', { name: 'How three-player RPS works' });
    expect(rules).toHaveTextContent('Three players, three gestures');
    expect(rules).toHaveTextContent('20-second limit');
    expect(rules).toHaveTextContent('first nine ordinary stakes');
    expect(rules).toHaveTextContent('latest 30 days');
    fireEvent.keyDown(document, { key: 'Escape' });
    expect(
      screen.queryByRole('dialog', { name: 'How three-player RPS works' }),
    ).not.toBeInTheDocument();
    await act(async () => {
      await rendered.i18n.changeLanguage('zh');
    });
    await rendered.user.click(screen.getByRole('button', { name: '玩法说明' }));
    const chineseRules = screen.getByRole('dialog', { name: '三人猜拳怎么玩' });
    expect(chineseRules).toHaveTextContent('三名玩家，三种手势');
    await rendered.user.click(within(chineseRules).getByRole('button', { name: '关闭玩法说明' }));
    expect(screen.queryByRole('dialog', { name: '三人猜拳怎么玩' })).not.toBeInTheDocument();
  });

  it('locks new RPS queue intent after an unknown response and replays the same identity', async () => {
    const snapshot = gamesSnapshotWire();
    snapshot.tutorial_rps_seen = true;
    const fetchMock = installJsonFetchFixtures([
      { method: 'GET', path: '/api/games', body: snapshot },
      {
        method: 'GET',
        path: '/api/games/rps/state',
        body: { kind: 'idle', tutorial_seen: true, modes: snapshot.rps.modes },
      },
      {
        method: 'GET',
        path: '/api/games/rps/leaderboard?mode=quick&board=profit_rate',
        body: emptyRPSBoard('quick', 'profit_rate'),
      },
      {
        method: 'GET',
        path: '/api/games/rps/leaderboard?mode=quick&board=net_profit',
        body: emptyRPSBoard('quick', 'net_profit'),
      },
    ]);
    const fixtures = fetchMock.getMockImplementation()!;
    fetchMock.mockImplementation(async (input, init) => {
      const target = new URL(String(input), window.location.origin);
      if (target.pathname === '/api/games/rps/queue' && init?.method === 'POST') {
        throw new TypeError('synthetic connection loss');
      }
      return fixtures(input, init);
    });
    const rendered = await renderWithProviders(<RPSGame />, {
      station: 'user',
      route: '/games/rps',
      role: 'user',
    });
    const join = await screen.findByRole('button', { name: 'Join Quick queue' });
    await rendered.user.click(join);
    expect(await screen.findByText(/result could not be confirmed/i)).toBeInTheDocument();
    expect(join).toBeDisabled();

    await rendered.user.click(screen.getByRole('button', { name: /Retry the same action/i }));
    await waitFor(() => {
      const queueCalls = fetchMock.mock.calls.filter(
        ([input, init]) =>
          new URL(String(input), window.location.origin).pathname === '/api/games/rps/queue' &&
          init?.method === 'POST',
      );
      expect(queueCalls).toHaveLength(2);
      expect((queueCalls[0][1]?.headers as Headers).get('Idempotency-Key')).toBe(
        (queueCalls[1][1]?.headers as Headers).get('Idempotency-Key'),
      );
      expect(queueCalls[0][1]?.body).toBe(queueCalls[1][1]?.body);
    });
  });

  it('re-reads authoritative RPS state instead of installing a stale queue receipt', async () => {
    const snapshot = gamesSnapshotWire();
    snapshot.tutorial_rps_seen = true;
    const state = rpsStateWire();
    state.mode = 'quick';
    const fetchMock = installJsonFetchFixtures([
      { method: 'GET', path: '/api/games', body: snapshot },
      {
        method: 'GET',
        path: '/api/games/rps/state',
        body: { kind: 'idle', tutorial_seen: true, modes: snapshot.rps.modes },
      },
      {
        method: 'POST',
        path: '/api/games/rps/queue',
        status: 202,
        body: {
          id: 'rpsq_AAAAAAAAAAAAAAAAAAAAAA',
          mode: 'quick',
          state: 'waiting',
          revision: '1',
          deadline: 1_800_000_120,
          server_now: 1_800_000_000,
        },
      },
      {
        method: 'POST',
        path: `/api/games/rps/sessions/${rpsTestSessionID}/lease`,
        body: { expires_at: 1_800_000_015 },
      },
      {
        method: 'GET',
        path: '/api/games/rps/leaderboard?mode=quick&board=profit_rate',
        body: emptyRPSBoard('quick', 'profit_rate'),
      },
      {
        method: 'GET',
        path: '/api/games/rps/leaderboard?mode=quick&board=net_profit',
        body: emptyRPSBoard('quick', 'net_profit'),
      },
    ]);
    const fixtures = fetchMock.getMockImplementation()!;
    let stateReads = 0;
    fetchMock.mockImplementation(async (input, init) => {
      const target = new URL(String(input), window.location.origin);
      if (target.pathname === '/api/games/rps/state' && (init?.method ?? 'GET') === 'GET') {
        stateReads += 1;
        if (stateReads > 1)
          return new Response(JSON.stringify({ kind: 'session', session: state }), {
            status: 200,
            headers: { 'content-type': 'application/json', 'cache-control': 'no-store' },
          });
      }
      return fixtures(input, init);
    });
    const rendered = await renderWithProviders(<RPSGame />, {
      station: 'user',
      route: '/games/rps',
      role: 'user',
    });
    await rendered.user.click(await screen.findByRole('button', { name: 'Join Quick queue' }));
    expect(await screen.findByText('Live match')).toBeInTheDocument();
    expect(screen.queryByText('Waiting for two more players')).not.toBeInTheDocument();
    expect(stateReads).toBeGreaterThanOrEqual(2);
  });

  it('keeps a newer identity epoch when a slower state GET resolves late', async () => {
    const snapshot = gamesSnapshotWire();
    snapshot.tutorial_rps_seen = true;
    const currentState = rpsStateWire('gesture', '7', '2');
    const staleState = rpsStateWire('gesture', '99', '1');
    const fetchMock = installJsonFetchFixtures([
      { method: 'GET', path: '/api/games', body: snapshot },
      {
        method: 'GET',
        path: '/api/games/rps/state',
        body: { kind: 'session', session: currentState },
      },
      {
        method: 'POST',
        path: `/api/games/rps/sessions/${rpsTestSessionID}/lease`,
        body: { expires_at: 1_800_000_015 },
      },
      {
        method: 'GET',
        path: '/api/games/rps/leaderboard?mode=standard&board=profit_rate',
        body: emptyRPSBoard('standard', 'profit_rate'),
      },
      {
        method: 'GET',
        path: '/api/games/rps/leaderboard?mode=standard&board=net_profit',
        body: emptyRPSBoard('standard', 'net_profit'),
      },
    ]);
    const fixtures = fetchMock.getMockImplementation()!;
    let reads = 0;
    fetchMock.mockImplementation(async (input, init) => {
      const target = new URL(String(input), window.location.origin);
      if (target.pathname === '/api/games/rps/state' && (init?.method ?? 'GET') === 'GET') {
        reads += 1;
        if (reads > 1)
          return new Response(JSON.stringify({ kind: 'session', session: staleState }), {
            status: 200,
            headers: { 'content-type': 'application/json', 'cache-control': 'no-store' },
          });
      }
      return fixtures(input, init);
    });
    const rendered = await renderWithProviders(<RPSGame />, {
      station: 'user',
      route: '/games/rps',
      role: 'user',
    });
    expect(await screen.findByText('Live match')).toBeInTheDocument();
    await act(async () => {
      await rendered.queryClient.refetchQueries({ queryKey: rpsKeys.state });
    });
    expect(rendered.queryClient.getQueryData(rpsKeys.state)).toMatchObject({
      kind: 'session',
      session: { identityEpoch: '2', revision: '7' },
    });
  });

  it('shows exact RPS commitments and blocks a mode the balance cannot cover', async () => {
    const snapshot = gamesSnapshotWire();
    snapshot.balance = '4';
    snapshot.tutorial_rps_seen = true;
    installJsonFetchFixtures([
      { method: 'GET', path: '/api/games', body: snapshot },
      {
        method: 'GET',
        path: '/api/games/rps/state',
        body: { kind: 'idle', tutorial_seen: true, modes: snapshot.rps.modes },
      },
      {
        method: 'GET',
        path: '/api/games/rps/leaderboard?mode=quick&board=profit_rate',
        body: emptyRPSBoard('quick', 'profit_rate'),
      },
      {
        method: 'GET',
        path: '/api/games/rps/leaderboard?mode=quick&board=net_profit',
        body: emptyRPSBoard('quick', 'net_profit'),
      },
      {
        method: 'GET',
        path: '/api/games/rps/leaderboard?mode=standard&board=profit_rate',
        body: emptyRPSBoard('standard', 'profit_rate'),
      },
      {
        method: 'GET',
        path: '/api/games/rps/leaderboard?mode=standard&board=net_profit',
        body: emptyRPSBoard('standard', 'net_profit'),
      },
      {
        method: 'GET',
        path: '/api/games/rps/leaderboard?mode=deathmatch&board=profit_rate',
        body: emptyRPSBoard('deathmatch', 'profit_rate'),
      },
      {
        method: 'GET',
        path: '/api/games/rps/leaderboard?mode=deathmatch&board=net_profit',
        body: emptyRPSBoard('deathmatch', 'net_profit'),
      },
    ]);
    const rendered = await renderWithProviders(<RPSGame />, {
      station: 'user',
      route: '/games/rps',
      role: 'user',
    });
    const standard = await screen.findByRole('button', { name: /Standard/ });
    expect(standard).toHaveTextContent('Entry amount: 10 credits');
    await rendered.user.click(standard);
    expect(screen.getByRole('button', { name: 'Join Standard queue' })).toBeDisabled();
    expect(screen.getByText(/cannot cover this mode’s entry amount/i)).toBeInTheDocument();

    const deathmatch = screen.getByRole('button', { name: /Deathmatch/ });
    expect(deathmatch).toHaveTextContent('Entry amount: 4 credits');
    await rendered.user.click(deathmatch);
    await rendered.user.click(screen.getByRole('button', { name: 'Join Deathmatch queue' }));
    const dialog = screen.getByRole('dialog');
    expect(dialog).toHaveTextContent('Entry amount: 4 credits');
    await rendered.user.click(standard);
    expect(dialog).toHaveTextContent('Entry amount: 4 credits');
    expect(within(dialog).getByRole('button', { name: /join deathmatch/i })).toBeEnabled();
  });

  it('keeps a complete private RPS result visible until its post-render ACK succeeds', async () => {
    vi.stubGlobal('requestAnimationFrame', (callback: FrameRequestCallback) =>
      window.setTimeout(() => callback(performance.now()), 0),
    );
    vi.stubGlobal('cancelAnimationFrame', (handle: number) => window.clearTimeout(handle));
    const snapshot = gamesSnapshotWire();
    snapshot.tutorial_rps_seen = true;
    const pending = {
      kind: 'pending_result',
      result: {
        session_id: rpsTestSessionID,
        mode: 'quick',
        terminal_reason: 'quick_resolved',
        own_seat_no: 0,
        own_input: '1',
        own_returned: '2',
        own_wallet_net: '1',
        seats: [
          { seat_no: 0, result: 'win' },
          { seat_no: 1, result: 'loss' },
          { seat_no: 2, result: 'deidentified' },
        ],
        created_at: 1_800_000_000,
      },
    };
    const fetchMock = installJsonFetchFixtures([
      { method: 'GET', path: '/api/games', body: snapshot },
      { method: 'GET', path: '/api/games/rps/state', body: pending },
      { method: 'POST', path: '/api/games/rps/pending-result/ack', status: 204, body: undefined },
    ]);
    await renderWithProviders(<RPSGame />, { station: 'user', route: '/games/rps', role: 'user' });
    expect(await screen.findByRole('heading', { name: 'Quick' })).toBeInTheDocument();
    expect(screen.getByText('Identity-hidden seat')).toBeInTheDocument();
    await waitFor(() =>
      expect(
        fetchMock.mock.calls.some(
          ([input, init]) =>
            String(input).endsWith('/api/games/rps/pending-result/ack') && init?.method === 'POST',
        ),
      ).toBe(true),
    );
  });

  it('keeps the pre-terminal lease long enough to render and ACK during maintenance', async () => {
    vi.stubGlobal('requestAnimationFrame', (callback: FrameRequestCallback) =>
      window.setTimeout(() => callback(performance.now()), 0),
    );
    vi.stubGlobal('cancelAnimationFrame', (handle: number) => window.clearTimeout(handle));
    const snapshot = gamesSnapshotWire();
    snapshot.tutorial_rps_seen = true;
    const fetchMock = installJsonFetchFixtures([
      { method: 'GET', path: '/api/games', body: snapshot },
      {
        method: 'GET',
        path: '/api/games/rps/state',
        body: { kind: 'session', session: rpsStateWire() },
      },
      {
        method: 'POST',
        path: `/api/games/rps/sessions/${rpsTestSessionID}/lease`,
        body: { expires_at: 1_800_000_015 },
      },
      {
        method: 'GET',
        path: '/api/games/rps/leaderboard?mode=standard&board=profit_rate',
        body: emptyRPSBoard('standard', 'profit_rate'),
      },
      {
        method: 'GET',
        path: '/api/games/rps/leaderboard?mode=standard&board=net_profit',
        body: emptyRPSBoard('standard', 'net_profit'),
      },
      { method: 'POST', path: '/api/games/rps/pending-result/ack', status: 204, body: undefined },
    ]);
    const fixtures = fetchMock.getMockImplementation()!;
    let maintenance = false;
    fetchMock.mockImplementation(async (input, init) => {
      const target = new URL(String(input), window.location.origin);
      if (maintenance && target.pathname === '/api/games') {
        return new Response(JSON.stringify({ error: { code: 'maintenance', message: 'down' } }), {
          status: 503,
          headers: { 'content-type': 'application/json' },
        });
      }
      return fixtures(input, init);
    });
    const rendered = await renderWithProviders(<RPSGame />, {
      station: 'user',
      route: '/games/rps',
      role: 'user',
    });
    expect(await screen.findByText('Live match')).toBeInTheDocument();
    await waitFor(() =>
      expect(
        fetchMock.mock.calls.some(
          ([input, init]) =>
            String(input).endsWith(`/api/games/rps/sessions/${rpsTestSessionID}/lease`) &&
            init?.method === 'POST',
        ),
      ).toBe(true),
    );
    maintenance = true;
    await act(async () => {
      await rendered.queryClient.refetchQueries({ queryKey: gameKeys.snapshot });
    });
    act(() => {
      rendered.queryClient.setQueryData(rpsKeys.state, normalizeRPSHome(rpsPendingResult()));
    });
    expect(await screen.findByText('Pending result')).toBeInTheDocument();
    expect(screen.getByText(/Maintenance is active/i)).toBeInTheDocument();
    await waitFor(() =>
      expect(
        fetchMock.mock.calls.some(
          ([input, init]) =>
            String(input).endsWith('/api/games/rps/pending-result/ack') && init?.method === 'POST',
        ),
      ).toBe(true),
    );
  });

  it('does not reveal or ACK a newly arriving private RPS result during maintenance', async () => {
    vi.stubGlobal('requestAnimationFrame', (callback: FrameRequestCallback) =>
      window.setTimeout(() => callback(performance.now()), 0),
    );
    vi.stubGlobal('cancelAnimationFrame', (handle: number) => window.clearTimeout(handle));
    const snapshot = gamesSnapshotWire();
    snapshot.tutorial_rps_seen = true;
    const fetchMock = installJsonFetchFixtures([
      { method: 'GET', path: '/api/games', body: snapshot },
      {
        method: 'GET',
        path: '/api/games/rps/state',
        body: { kind: 'idle', tutorial_seen: true, modes: snapshot.rps.modes },
      },
      {
        method: 'GET',
        path: '/api/games/rps/leaderboard?mode=quick&board=profit_rate',
        body: emptyRPSBoard('quick', 'profit_rate'),
      },
      {
        method: 'GET',
        path: '/api/games/rps/leaderboard?mode=quick&board=net_profit',
        body: emptyRPSBoard('quick', 'net_profit'),
      },
    ]);
    const fixtures = fetchMock.getMockImplementation()!;
    let maintenance = false;
    fetchMock.mockImplementation(async (input, init) => {
      const target = new URL(String(input), window.location.origin);
      if (maintenance && target.pathname === '/api/games') {
        return new Response(JSON.stringify({ error: { code: 'maintenance', message: 'down' } }), {
          status: 503,
          headers: { 'content-type': 'application/json' },
        });
      }
      return fixtures(input, init);
    });
    const rendered = await renderWithProviders(<RPSGame />, {
      station: 'user',
      route: '/games/rps',
      role: 'user',
    });
    expect(await screen.findByRole('button', { name: 'Join Quick queue' })).toBeInTheDocument();
    maintenance = true;
    await act(async () => {
      await rendered.queryClient.refetchQueries({ queryKey: gameKeys.snapshot });
    });
    act(() => {
      rendered.queryClient.setQueryData(
        rpsKeys.state,
        normalizeRPSHome({
          kind: 'pending_result',
          result: {
            session_id: rpsTestSessionID,
            mode: 'quick',
            terminal_reason: 'quick_resolved',
            own_seat_no: 0,
            own_input: '1',
            own_returned: '2',
            own_wallet_net: '1',
            seats: [
              { seat_no: 0, result: 'win' },
              { seat_no: 1, result: 'loss' },
              { seat_no: 2, result: 'loss' },
            ],
            created_at: 1_800_000_000,
          },
        }),
      );
    });
    expect(await screen.findByText('Maintenance')).toBeInTheDocument();
    expect(screen.queryByText('Pending result')).not.toBeInTheDocument();
    await new Promise((resolve) => window.setTimeout(resolve, 20));
    expect(
      fetchMock.mock.calls.some(
        ([input, init]) =>
          String(input).endsWith('/api/games/rps/pending-result/ack') && init?.method === 'POST',
      ),
    ).toBe(false);
  });
});
