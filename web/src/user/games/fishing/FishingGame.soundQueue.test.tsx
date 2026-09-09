import { act, fireEvent, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { installJsonFetchFixtures, renderWithProviders } from '../../../../test/unit/support';
import { gamesSnapshotWire } from '../common/testFixtures';
import { FishingGame } from './FishingGame';

const playSound = vi.hoisted(() => vi.fn());
const toggleSound = vi.hoisted(() => vi.fn());

vi.mock('../common/useGameSound', () => ({
  useGameSound: () => ({ enabled: true, toggle: toggleSound, play: playSound }),
}));

const OLD_BATCH_ID = 'fb_AAAAAAAAAAAAAAAAAAAAAA';
const NEW_BATCH_ID = 'fb_AQEBAQEBAQEBAQEBAQEBAQ';

function resultWire(batchID: string, balance = '12345678901234567890.125') {
  return {
    batch_id: batchID,
    bait: 'worm',
    count: 1,
    unit_price: '1',
    entry_total: '1',
    outcomes: [
      {
        ordinal: 0,
        species_key: 'whitebait',
        tier: 'small',
        size_cm: 12,
        reward: '1',
      },
    ],
    payout_total: '1',
    balance,
    settled_at: 1_800_000_000,
    idempotent_replay: false,
  };
}

function stateWire(result: unknown) {
  return { settlement_pending: null, unrevealed: result, has_more_unrevealed: false };
}

function emptyStateWire() {
  return stateWire(null);
}

function emptyBoard(board: 'single' | 'total') {
  return { board, window_start: board === 'single' ? null : 1_700_000_000, entries: [], me: null };
}

function fishingFixtures(state: unknown) {
  return [
    { method: 'GET', path: '/api/games', body: gamesSnapshotWire() },
    { method: 'GET', path: '/api/games/fishing/state', body: state },
    {
      method: 'GET',
      path: '/api/games/fishing/leaderboard?board=single',
      body: emptyBoard('single'),
    },
    {
      method: 'GET',
      path: '/api/games/fishing/leaderboard?board=total',
      body: emptyBoard('total'),
    },
  ] as const;
}

function stubReducedMotion(reduced: boolean) {
  vi.stubGlobal('matchMedia', (query: string) => ({
    matches: reduced && query === '(prefers-reduced-motion: reduce)',
    media: query,
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
  }));
}

function visibleBatchID(): string | undefined {
  return document.querySelector<HTMLElement>('[data-batch-id]')?.dataset.batchId;
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(body === undefined ? undefined : JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json' },
  });
}

async function flushZeroTimers() {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(0);
    await Promise.resolve();
    await Promise.resolve();
    await Promise.resolve();
  });
}

function catchCalls() {
  return playSound.mock.calls.filter(([cue]) =>
    cue === 'fishing_common' || cue === 'fishing_rare' || cue === 'fishing_epic',
  );
}

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
  playSound.mockReset();
  toggleSound.mockReset();
});

describe('Fishing sound and authoritative result queue', () => {
  it('keeps history silent, advances A to B once after ACK recovery, and never repeats B', async () => {
    vi.useFakeTimers();
    stubReducedMotion(true);
    const oldResult = resultWire(OLD_BATCH_ID);
    const newResult = resultWire(NEW_BATCH_ID);
    const refreshedNewResult = resultWire(NEW_BATCH_ID, '12345678901234567880.125');
    let stateReads = 0;
    let startCalls = 0;
    let oldAckAttempts = 0;
    let newAckAttempts = 0;
    let oldAcknowledged = false;
    let newAcknowledged = false;
    let balanceRefreshed = false;
    const fetchMock = installJsonFetchFixtures(fishingFixtures(stateWire(oldResult)));
    const fixtureFetch = fetchMock.getMockImplementation()!;
    fetchMock.mockImplementation(async (input, init) => {
      const target = new URL(String(input), window.location.origin);
      const method = init?.method ?? 'GET';
      if (target.pathname === '/api/games/fishing/state' && method === 'GET') {
        stateReads += 1;
        if (newAcknowledged) return jsonResponse(emptyStateWire());
        if (oldAcknowledged)
          return jsonResponse(stateWire(balanceRefreshed ? refreshedNewResult : newResult));
        return jsonResponse(stateWire(oldResult));
      }
      if (
        target.pathname === `/api/games/fishing/batches/${OLD_BATCH_ID}/ack` &&
        method === 'POST'
      ) {
        oldAckAttempts += 1;
        if (oldAckAttempts === 1)
          return jsonResponse({ error: { code: 'rate_limited', message: 'retry later' } }, 429);
        oldAcknowledged = true;
        return jsonResponse(undefined, 204);
      }
      if (
        target.pathname === `/api/games/fishing/batches/${NEW_BATCH_ID}/ack` &&
        method === 'POST'
      ) {
        newAckAttempts += 1;
        if (newAckAttempts === 1)
          return jsonResponse({ error: { code: 'rate_limited', message: 'retry later' } }, 429);
        newAcknowledged = true;
        return jsonResponse(undefined, 204);
      }
      if (target.pathname === '/api/games/fishing/batches' && method === 'POST') {
        startCalls += 1;
        return jsonResponse(newResult);
      }
      return fixtureFetch(input, init);
    });

    const rendered = await renderWithProviders(<FishingGame />, {
      station: 'user',
      route: '/games/fishing',
      role: 'user',
    });
    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
      await Promise.resolve();
      await vi.advanceTimersByTimeAsync(10);
      await Promise.resolve();
      await Promise.resolve();
    });
    expect(visibleBatchID()).toBe(OLD_BATCH_ID);
    expect(catchCalls()).toHaveLength(0);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(650);
    });
    await flushZeroTimers();
    expect(oldAckAttempts).toBe(1);
    expect(screen.getByRole('button', { name: 'Retry marking as viewed' })).toBeInTheDocument();
    expect(catchCalls()).toHaveLength(0);

    const start = screen.getByRole('button', { name: 'Start fishing' });
    expect(start).toBeEnabled();
    act(() => {
      fireEvent.click(start);
      fireEvent.click(start);
      fireEvent.click(start);
    });
    await flushZeroTimers();
    expect(startCalls).toBe(1);
    expect(visibleBatchID()).toBe(OLD_BATCH_ID);
    expect(catchCalls()).toHaveLength(0);

    fireEvent.click(screen.getByRole('button', { name: 'Retry marking as viewed' }));
    await flushZeroTimers();
    expect(oldAckAttempts).toBe(2);
    expect(visibleBatchID()).toBe(NEW_BATCH_ID);
    expect(catchCalls()).toHaveLength(1);
    expect(catchCalls()[0]).toEqual(['fishing_common']);

    balanceRefreshed = true;
    await act(async () => {
      await rendered.queryClient.invalidateQueries({ queryKey: ['user', 'games', 'fishing', 'state'] });
    });
    await flushZeroTimers();
    expect(stateReads).toBeGreaterThanOrEqual(3);
    expect(visibleBatchID()).toBe(NEW_BATCH_ID);
    expect(catchCalls()).toHaveLength(1);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(650);
    });
    await flushZeroTimers();
    expect(newAckAttempts).toBe(1);
    expect(screen.getByRole('button', { name: 'Retry marking as viewed' })).toBeInTheDocument();
    expect(catchCalls()).toHaveLength(1);

    fireEvent.click(screen.getByRole('button', { name: 'Retry marking as viewed' }));
    await flushZeroTimers();
    expect(newAckAttempts).toBe(2);
    expect(catchCalls()).toHaveLength(1);
    rendered.unmount();
  });
});
