import { act, fireEvent, screen, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { installJsonFetchFixtures, renderWithProviders } from '../../../../test/unit/support';
import { FishingGame } from './FishingGame';
import { fishingKeys } from './api';
import { FISHING_REVEAL_MS } from './stateMachine';
import { gamesSnapshotWire } from '../common/testFixtures';

const OLD_BATCH_ID = 'fb_AAAAAAAAAAAAAAAAAAAAAA';
const NEW_BATCH_ID = 'fb_AQEBAQEBAQEBAQEBAQEBAQ';

function resultWire(batchID: string, count: 1 | 10) {
  const outcomes = Array.from({ length: count }, (_, ordinal) => ({
    ordinal,
    species_key: 'whitebait',
    tier: 'small',
    size_cm: 12,
    reward: '1', net_reward: '1', rake: { platform: '0', welfare: '0', thursday: '0' },
  }));
  return {
    batch_id: batchID,
    bait: 'worm',
    count,
    unit_price: '1',
    entry_total: String(count),
    rules_version: 1,
    payment: { general: String(count), game: '0' },
    outcomes,
    payout_total: String(count), net_payout_total: String(count), rake: { platform: '0', welfare: '0', thursday: '0' },
    balance: '12345678901234567890.125',
    game_balance: '0',
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

function emptyBoard(board: 'single' | 'recent_single' | 'total') {
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
      path: '/api/games/fishing/leaderboard?board=recent_single',
      body: emptyBoard('recent_single'),
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

function pendingWire(batchID = NEW_BATCH_ID) {
  return {
    batch_id: batchID,
    bait: 'worm',
    count: 1,
    entry_total: '1',
    rules_version: 1,
    payment: { general: '1', game: '0' },
    state: 'settlement_pending',
    next_attempt_at: 1_800_000_120,
    retry_exhausted: false,
  };
}

async function flushInitialFishing() {
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
    await Promise.resolve();
    await vi.advanceTimersByTimeAsync(10);
    await Promise.resolve();
    await Promise.resolve();
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

afterEach(() => vi.useRealTimers());

describe('Fishing result presentation and queue recovery', () => {
  it('shows net general income with expandable deductions and spends a positive game wallet independently', async () => {
    vi.useFakeTimers();
    stubReducedMotion(true);
    const result = resultWire(OLD_BATCH_ID, 1);
    result.rules_version = 2;
    result.net_payout_total = '0.97';
    result.rake = { platform: '0.01', welfare: '0.01', thursday: '0.01' };
    result.outcomes[0].net_reward = '0.97';
    result.outcomes[0].rake = { ...result.rake };
    const snapshot = { ...gamesSnapshotWire(), balance: '-100', game_balance: '10' };
    installJsonFetchFixtures(fishingFixtures(stateWire(result)).map((fixture) =>
      fixture.path === '/api/games' ? { ...fixture, body: snapshot } : fixture));
    const rendered = await renderWithProviders(<FishingGame />, { station: 'user', route: '/games/fishing', role: 'user' });
    await flushInitialFishing();
    expect(screen.getByRole('button', { name: 'Start fishing' })).toBeEnabled();
    const catchList = within(screen.getByRole('list', { name: 'Your catch is ready' }));
    expect(catchList.getByText('Net: 0.97 general credits')).toBeVisible();
    const details = catchList.getByText('Gross catch and deductions').closest('details')!;
    expect(details).not.toHaveAttribute('open');
    fireEvent.click(within(details).getByText('Gross catch and deductions'));
    expect(details).toHaveAttribute('open');
    expect(within(details).getByText('Welfare pool deduction')).toBeVisible();
    rendered.unmount();
  });

  it('disables start until the previous batch is fully presented and ignores rapid clicks', async () => {
    vi.useFakeTimers();
    stubReducedMotion(false);
    const oldResult = resultWire(OLD_BATCH_ID, 10);
    const fetchMock = installJsonFetchFixtures([
      ...fishingFixtures(stateWire(oldResult)),
      {
        method: 'POST',
        path: '/api/games/fishing/batches',
        status: 202,
        body: pendingWire(),
      },
    ]);

    const rendered = await renderWithProviders(<FishingGame />, {
      station: 'user',
      route: '/games/fishing',
      role: 'user',
    });
    await flushInitialFishing();
    const start = screen.getByRole('button', { name: 'Start fishing' });
    expect(visibleBatchID()).toBe(OLD_BATCH_ID);
    expect(within(screen.getByRole('list', { name: 'Your catch is ready' })).queryAllByRole('listitem')).toHaveLength(0);
    expect(start).toBeDisabled();
    fireEvent.click(start);
    fireEvent.click(start);
    await flushZeroTimers();

    const starts = fetchMock.mock.calls.filter(
      ([input, init]) =>
        new URL(String(input), window.location.origin).pathname === '/api/games/fishing/batches' &&
        init?.method === 'POST',
    );
    expect(starts).toHaveLength(0);
    expect(visibleBatchID()).toBe(OLD_BATCH_ID);
    rendered.unmount();
  });

  it('keeps the oldest failed result visible while a newer start cannot skip the authoritative queue', async () => {
    vi.useFakeTimers();
    stubReducedMotion(true);
    const oldResult = resultWire(OLD_BATCH_ID, 1);
    const newResult = resultWire(NEW_BATCH_ID, 1);
    let releaseStartResponse!: () => void;
    const startResponseReleased = new Promise<void>((resolve) => {
      releaseStartResponse = resolve;
    });
    let releaseStaleState!: () => void;
    const staleStateReleased = new Promise<void>((resolve) => {
      releaseStaleState = resolve;
    });
    let stateReads = 0;
    let startRequested = false;
    let firstAcknowledgements = 0;
    const fetchMock = installJsonFetchFixtures(fishingFixtures(stateWire(oldResult)));
    const fixtureFetch = fetchMock.getMockImplementation()!;
    fetchMock.mockImplementation(async (input, init) => {
      const target = new URL(String(input), window.location.origin);
      const method = init?.method ?? 'GET';
      if (target.pathname === '/api/games/fishing/state' && method === 'GET') {
        stateReads += 1;
        if (startRequested && stateReads >= 3) await staleStateReleased;
        return jsonResponse(stateWire(oldResult));
      }
      if (
        target.pathname === `/api/games/fishing/batches/${OLD_BATCH_ID}/ack` &&
        method === 'POST'
      ) {
        firstAcknowledgements += 1;
        return firstAcknowledgements === 1
          ? jsonResponse({ error: { code: 'rate_limited', message: 'retry later' } }, 429)
          : jsonResponse(undefined, 204);
      }
      if (target.pathname === '/api/games/fishing/batches' && method === 'POST') {
        startRequested = true;
        await startResponseReleased;
        return jsonResponse(newResult);
      }
      return fixtureFetch(input, init);
    });

    const rendered = await renderWithProviders(<FishingGame />, {
      station: 'user',
      route: '/games/fishing',
      role: 'user',
    });
    await flushInitialFishing();
    expect(visibleBatchID()).toBe(OLD_BATCH_ID);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(650);
    });
    expect(firstAcknowledgements).toBe(1);
    expect(screen.getByRole('button', { name: 'Retry marking as viewed' })).toBeInTheDocument();
    expect(visibleBatchID()).toBe(OLD_BATCH_ID);

    const start = screen.getByRole('button', { name: 'Start fishing' });
    expect(start).toBeEnabled();
    act(() => {
      fireEvent.click(start);
      fireEvent.click(start);
      fireEvent.click(start);
    });
    expect(start).toBeDisabled();
    expect(
      fetchMock.mock.calls.filter(
        ([input, init]) =>
          String(input).endsWith('/api/games/fishing/batches') && init?.method === 'POST',
      ),
    ).toHaveLength(1);
    expect(visibleBatchID()).toBe(OLD_BATCH_ID);
    releaseStartResponse();
    for (let attempt = 0; attempt < 5 && stateReads < 3; attempt += 1) await flushZeroTimers();
    expect(stateReads).toBeGreaterThanOrEqual(3);
    // The start response is newer, but the state endpoint still reports the
    // oldest unacknowledged batch. The UI must keep that batch visible until
    // it is acknowledged instead of creating a new→old flashback.
    expect(visibleBatchID()).toBe(OLD_BATCH_ID);
    releaseStaleState();
    await flushZeroTimers();
    expect(visibleBatchID()).toBe(OLD_BATCH_ID);
    expect(firstAcknowledgements).toBe(1);
    rendered.unmount();
  });

  it('keeps an old result visible and retries an unknown start with the same identity', async () => {
    vi.useFakeTimers();
    stubReducedMotion(true);
    const oldResult = resultWire(OLD_BATCH_ID, 1);
    const fetchMock = installJsonFetchFixtures(fishingFixtures(stateWire(oldResult)));
    const fixtureFetch = fetchMock.getMockImplementation()!;
    fetchMock.mockImplementation(async (input, init) => {
      const target = new URL(String(input), window.location.origin);
      if (target.pathname === '/api/games/fishing/batches' && (init?.method ?? 'GET') === 'POST')
        throw new TypeError('synthetic connection loss');
      return fixtureFetch(input, init);
    });

    const rendered = await renderWithProviders(<FishingGame />, {
      station: 'user',
      route: '/games/fishing',
      role: 'user',
    });
    await flushInitialFishing();
    const start = screen.getByRole('button', { name: 'Start fishing' });
    expect(visibleBatchID()).toBe(OLD_BATCH_ID);
    fireEvent.click(start);
    await flushZeroTimers();
    expect(visibleBatchID()).toBe(OLD_BATCH_ID);
    expect(screen.getByText(/result could not be confirmed/i)).toBeInTheDocument();
    expect(start).toBeDisabled();
    fireEvent.click(screen.getByRole('button', { name: /retry the same action/i }));
    await flushZeroTimers();
    const starts = fetchMock.mock.calls.filter(
      ([input, init]) =>
        new URL(String(input), window.location.origin).pathname === '/api/games/fishing/batches' &&
        init?.method === 'POST',
    );
    expect(starts).toHaveLength(2);
    expect(new Headers(starts[0][1]?.headers).get('Idempotency-Key')).toBeTruthy();
    expect(new Headers(starts[0][1]?.headers).get('Idempotency-Key')).toBe(
      new Headers(starts[1][1]?.headers).get('Idempotency-Key'),
    );
    expect(starts[0][1]?.body).toBe(starts[1][1]?.body);
    rendered.unmount();
  });

  it('does not restart a same-batch result reveal after a balance refresh', async () => {
    vi.useFakeTimers();
    stubReducedMotion(false);
    const oldResult = resultWire(OLD_BATCH_ID, 10);
    const updatedResult = { ...oldResult, balance: '12345678901234567880.125' };
    const fetchMock = installJsonFetchFixtures(fishingFixtures(stateWire(oldResult)));
    const fixtureFetch = fetchMock.getMockImplementation()!;
    let stateReads = 0;
    fetchMock.mockImplementation(async (input, init) => {
      const target = new URL(String(input), window.location.origin);
      if (target.pathname === '/api/games/fishing/state' && (init?.method ?? 'GET') === 'GET') {
        stateReads += 1;
        return jsonResponse(stateWire(stateReads >= 2 ? updatedResult : oldResult));
      }
      return fixtureFetch(input, init);
    });
    const rendered = await renderWithProviders(<FishingGame />, {
      station: 'user',
      route: '/games/fishing',
      role: 'user',
    });
    await flushInitialFishing();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(FISHING_REVEAL_MS);
    });
    expect(within(screen.getByRole('list', { name: 'Your catch is ready' })).getAllByRole('listitem')).toHaveLength(1);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(200);
    });
    await act(async () => {
      await rendered.queryClient.invalidateQueries({ queryKey: fishingKeys.state });
    });
    await flushZeroTimers();
    expect(stateReads).toBeGreaterThanOrEqual(2);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(220);
    });
    expect(within(screen.getByRole('list', { name: 'Your catch is ready' })).getAllByRole('listitem')).toHaveLength(2);
    rendered.unmount();
  });

  it('does not restart the same-batch ACK delay after a balance refresh', async () => {
    vi.useFakeTimers();
    stubReducedMotion(true);
    const oldResult = resultWire(OLD_BATCH_ID, 1);
    const updatedResult = { ...oldResult, balance: '12345678901234567880.125' };
    const fetchMock = installJsonFetchFixtures(fishingFixtures(stateWire(oldResult)));
    const fixtureFetch = fetchMock.getMockImplementation()!;
    let stateReads = 0;
    let acknowledgements = 0;
    fetchMock.mockImplementation(async (input, init) => {
      const target = new URL(String(input), window.location.origin);
      const method = init?.method ?? 'GET';
      if (target.pathname === '/api/games/fishing/state' && method === 'GET') {
        stateReads += 1;
        return jsonResponse(stateWire(stateReads >= 2 ? updatedResult : oldResult));
      }
      if (
        target.pathname === `/api/games/fishing/batches/${OLD_BATCH_ID}/ack` &&
        method === 'POST'
      ) {
        acknowledgements += 1;
        return jsonResponse(undefined, 204);
      }
      return fixtureFetch(input, init);
    });
    const rendered = await renderWithProviders(<FishingGame />, {
      station: 'user',
      route: '/games/fishing',
      role: 'user',
    });
    await flushInitialFishing();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(300);
    });
    await act(async () => {
      await rendered.queryClient.invalidateQueries({ queryKey: fishingKeys.state });
    });
    await flushZeroTimers();
    expect(stateReads).toBeGreaterThanOrEqual(2);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(350);
    });
    await flushZeroTimers();
    expect(acknowledgements).toBe(1);
    rendered.unmount();
  });

  it('advances queued batches in order and does not replay after a transient ACK 429', async () => {
    vi.useFakeTimers();
    stubReducedMotion(true);
    const firstResult = resultWire(OLD_BATCH_ID, 1);
    const secondResult = resultWire(NEW_BATCH_ID, 1);
    const fetchMock = installJsonFetchFixtures(fishingFixtures(stateWire(firstResult)));
    const fixtureFetch = fetchMock.getMockImplementation()!;
    let firstAcknowledgements = 0;
    let secondAcknowledgements = 0;
    let firstAcknowledged = false;
    let secondAcknowledged = false;
    let resolveEmptyState!: () => void;
    const emptyStateRead = new Promise<void>((resolve) => {
      resolveEmptyState = resolve;
    });
    fetchMock.mockImplementation(async (input, init) => {
      const target = new URL(String(input), window.location.origin);
      const method = init?.method ?? 'GET';
      if (target.pathname === '/api/games/fishing/state' && method === 'GET') {
        if (secondAcknowledged) {
          resolveEmptyState();
          return jsonResponse(emptyStateWire());
        }
        return jsonResponse(stateWire(firstAcknowledged ? secondResult : firstResult));
      }
      if (
        target.pathname === `/api/games/fishing/batches/${OLD_BATCH_ID}/ack` &&
        method === 'POST'
      ) {
        firstAcknowledgements += 1;
        if (firstAcknowledgements === 1) {
          return jsonResponse({ error: { code: 'rate_limited', message: 'retry later' } }, 429);
        }
        firstAcknowledged = true;
        return jsonResponse(undefined, 204);
      }
      if (
        target.pathname === `/api/games/fishing/batches/${NEW_BATCH_ID}/ack` &&
        method === 'POST'
      ) {
        secondAcknowledgements += 1;
        secondAcknowledged = true;
        return jsonResponse(undefined, 204);
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
    const displayedBatchIDs = [visibleBatchID()];
    await act(async () => {
      await vi.advanceTimersByTimeAsync(650);
    });
    expect(screen.getByRole('button', { name: 'Retry marking as viewed' })).toBeInTheDocument();
    expect(firstAcknowledgements).toBe(1);
    expect(screen.getByRole('list', { name: 'Your catch is ready' })).toBeInTheDocument();

    await act(async () => {
      await vi.advanceTimersByTimeAsync(2_000);
    });
    expect(firstAcknowledgements).toBe(1);

    fireEvent.click(screen.getByRole('button', { name: 'Retry marking as viewed' }));
    for (let attempt = 0; attempt < 5 && visibleBatchID() !== NEW_BATCH_ID; attempt += 1) {
      await act(async () => {
        await vi.advanceTimersByTimeAsync(0);
        await Promise.resolve();
        await Promise.resolve();
      });
    }
    expect(visibleBatchID()).toBe(NEW_BATCH_ID);
    displayedBatchIDs.push(visibleBatchID());
    await act(async () => {
      await vi.advanceTimersByTimeAsync(650);
    });
    await emptyStateRead;
    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
    });
    expect(firstAcknowledgements).toBe(2);
    expect(secondAcknowledgements).toBe(1);
    expect(displayedBatchIDs).toEqual([OLD_BATCH_ID, NEW_BATCH_ID]);
    expect(rendered.queryClient.getQueryData(fishingKeys.state)).toMatchObject({
      settlementPending: null,
      unrevealed: null,
      hasMoreUnrevealed: false,
    });

    await act(async () => {
      await vi.advanceTimersByTimeAsync(2_000);
    });
    expect(firstAcknowledgements).toBe(2);
    expect(secondAcknowledgements).toBe(1);
    rendered.unmount();
  });
});
