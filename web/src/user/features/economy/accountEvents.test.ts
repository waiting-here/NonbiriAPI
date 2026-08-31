import { afterEach, describe, expect, it, vi } from 'vitest';
import { connectActivityAccountEvents, parseActivityAccountEvent } from './accountEvents';

const EVENT_ID = 'sse_abcdefghijklmnopqrstuA';
const EVENT_ID_2 = 'sse_abcdefghijklmnopqrstuQ';
const EVENT_ID_3 = 'sse_abcdefghijklmnopqrstug';
const EVENT_ID_4 = 'sse_abcdefghijklmnopqrstuw';
const ACTIVITY_FIXTURE = {
  master: { enabled: true, available: true, reason: 'available' },
  welfare: {
    enabled: true,
    state: 'available',
    site_day: '2026-08-31',
    threshold: '10',
    cap: '2.5',
    pool_balance: '9.999',
    claimed_today: false,
  },
  thursday: {
    enabled: true,
    state: 'open',
    server_now: 1_788_111_000,
    current: {
      period_id: 'thu_abcdefghijklmnopqrstuA',
      revision: '7',
      opens_at: 1_788_110_000,
      closes_at: 1_788_196_400,
      literature: 'plain text',
      entry: '50',
      per_user_limit: 3,
      pool_balance: '123.456',
      my_count: '1',
      my_contributed: '50',
    },
    next: null,
    last_result: null,
  },
} as const;

function frame(
  type: 'snapshot' | 'delta' | 'gap',
  channel: 'activities' | 'rps' = 'activities',
  revision = '8',
) {
  return {
    version: 1,
    channel,
    type,
    revision: type === 'gap' ? null : revision,
    identity_epoch: null,
    occurred_at: 1_788_111_001,
    data: type === 'gap' ? { reason: 'ring_expired', last_event_id: EVENT_ID } : ACTIVITY_FIXTURE,
  };
}

function sseRecord(type: 'snapshot' | 'delta' | 'gap', id: string, value = frame(type)): string {
  return `id: ${id}\nevent: ${type}\ndata: ${JSON.stringify(value)}\n\n`;
}

function streamResponse(body: string, status = 200): Response {
  return new Response(body, {
    status,
    headers: { 'Content-Type': 'text/event-stream; charset=utf-8' },
  });
}

async function flushAsyncWork(): Promise<void> {
  for (let index = 0; index < 8; index += 1) await Promise.resolve();
  await vi.advanceTimersByTimeAsync(0);
  for (let index = 0; index < 8; index += 1) await Promise.resolve();
}

afterEach(() => {
  vi.useRealTimers();
});

describe('activities account events', () => {
  it('uses both snapshot and delta as complete authoritative replacements', () => {
    for (const type of ['snapshot', 'delta'] as const) {
      const result = parseActivityAccountEvent(type, JSON.stringify(frame(type)), EVENT_ID);
      expect(result.kind).toBe('replace');
      if (result.kind === 'replace') {
        expect(result.revision).toBe('8');
        expect(result.snapshot.thursday.current?.entry).toBe('50');
      }
    }
  });

  it('forces a GET resync for gap, malformed frames, and invalid event ids', () => {
    expect(parseActivityAccountEvent('gap', JSON.stringify(frame('gap')), EVENT_ID)).toEqual({
      kind: 'resync',
      reason: 'gap',
    });
    expect(parseActivityAccountEvent('snapshot', '{', EVENT_ID)).toEqual({
      kind: 'resync',
      reason: 'malformed',
    });
    expect(parseActivityAccountEvent('snapshot', JSON.stringify(frame('snapshot')), '8')).toEqual({
      kind: 'resync',
      reason: 'malformed',
    });
    expect(
      parseActivityAccountEvent(
        'snapshot',
        JSON.stringify({
          ...frame('snapshot'),
          data: { ...ACTIVITY_FIXTURE, pool_source_events: [] },
        }),
        EVENT_ID,
      ),
    ).toEqual({ kind: 'resync', reason: 'malformed' });
    const maximumRevision = '340282366920938463463374607431768211455';
    const maximum = parseActivityAccountEvent(
      'snapshot',
      JSON.stringify({ ...frame('snapshot'), revision: maximumRevision }),
      EVENT_ID,
    );
    expect(maximum.kind).toBe('replace');
    if (maximum.kind === 'replace') expect(maximum.revision).toBe(maximumRevision);
    expect(
      parseActivityAccountEvent(
        'snapshot',
        JSON.stringify({
          ...frame('snapshot'),
          revision: '340282366920938463463374607431768211456',
        }),
        EVENT_ID,
      ),
    ).toEqual({ kind: 'resync', reason: 'malformed' });
    expect(parseActivityAccountEvent('snapshot', 'x'.repeat(64 * 1024 + 1), EVENT_ID)).toEqual({
      kind: 'resync',
      reason: 'malformed',
    });
    expect(parseActivityAccountEvent('delta', 'x'.repeat(16 * 1024 + 1), EVENT_ID)).toEqual({
      kind: 'resync',
      reason: 'malformed',
    });
  });

  it('ignores valid RPS frames without interpreting their payload', () => {
    const rps = {
      ...frame('delta', 'rps'),
      data: { participant: 'must-not-enter-activity-cache' },
    };
    expect(parseActivityAccountEvent('delta', JSON.stringify(rps), EVENT_ID)).toEqual({
      kind: 'ignore',
    });
  });

  it('replays with Last-Event-ID, replaces complete snapshots, and reconciles gaps', async () => {
    vi.useFakeTimers();
    const onResync = vi.fn();
    const onReplace = vi.fn();
    const fetchStream = vi
      .fn<(input: RequestInfo | URL, init?: RequestInit) => Promise<Response>>()
      .mockResolvedValueOnce(
        streamResponse(
          sseRecord('delta', EVENT_ID) +
            sseRecord('snapshot', EVENT_ID_2, frame('snapshot', 'activities', '7')) +
            sseRecord('gap', EVENT_ID_3) +
            sseRecord('snapshot', EVENT_ID_4),
        ),
      )
      .mockImplementation(() => new Promise<Response>(() => undefined));
    const disconnect = connectActivityAccountEvents({
      onReplace,
      onResync,
      fetchStream,
      random: () => 1,
    });
    await flushAsyncWork();
    expect(fetchStream).toHaveBeenCalledTimes(1);
    expect(fetchStream.mock.calls[0]?.[0]).toBe('/api/events');
    expect(new Headers(fetchStream.mock.calls[0]?.[1]?.headers).get('Last-Event-ID')).toBeNull();
    expect(onResync).toHaveBeenCalledWith('gap');
    expect(onResync).toHaveBeenCalledWith('disconnect');
    expect(onResync.mock.calls.filter(([reason]) => reason === 'disconnect')).toHaveLength(1);
    expect(onReplace).toHaveBeenCalledTimes(2);
    await vi.advanceTimersByTimeAsync(1_000);
    expect(fetchStream).toHaveBeenCalledTimes(2);
    const replayHeaders = new Headers(fetchStream.mock.calls[1]?.[1]?.headers);
    expect(replayHeaders.get('Last-Event-ID')).toBe(EVENT_ID_4);
    disconnect();
    expect(fetchStream.mock.calls[1]?.[1]?.signal?.aborted).toBe(true);
  });

  it('uses jittered 1/2/4/8/15 second reconnect ceilings and deduplicates disconnect GETs', async () => {
    vi.useFakeTimers();
    const fetchStream = vi.fn(() => Promise.reject(new TypeError('offline')));
    const onResync = vi.fn();
    const disconnect = connectActivityAccountEvents({
      onReplace: vi.fn(),
      onResync,
      fetchStream,
      random: () => 1,
    });
    await flushAsyncWork();
    expect(fetchStream).toHaveBeenCalledTimes(1);
    for (const [index, ceiling] of [1_000, 2_000, 4_000, 8_000, 15_000, 15_000].entries()) {
      await vi.advanceTimersByTimeAsync(ceiling - 1);
      expect(fetchStream).toHaveBeenCalledTimes(index + 1);
      await vi.advanceTimersByTimeAsync(1);
      await flushAsyncWork();
      expect(fetchStream).toHaveBeenCalledTimes(index + 2);
    }
    expect(onResync.mock.calls.filter(([reason]) => reason === 'disconnect')).toHaveLength(1);
    disconnect();

    const jitteredFetch = vi.fn(() => Promise.reject(new TypeError('offline')));
    const stopJittered = connectActivityAccountEvents({
      onReplace: vi.fn(),
      onResync: vi.fn(),
      fetchStream: jitteredFetch,
      random: () => 0,
    });
    await flushAsyncWork();
    await vi.advanceTimersByTimeAsync(799);
    expect(jitteredFetch).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(1);
    await flushAsyncWork();
    expect(jitteredFetch).toHaveBeenCalledTimes(2);
    stopJittered();
  });

  it('lets focus perform one immediate reconnect and rejects malformed SSE control fields', async () => {
    vi.useFakeTimers();
    const fetchStream = vi
      .fn<(input: RequestInfo | URL, init?: RequestInit) => Promise<Response>>()
      .mockRejectedValueOnce(new TypeError('offline'))
      .mockResolvedValueOnce(
        streamResponse(
          `id: ${EVENT_ID}\nevent: delta\nevent: delta\ndata: ${JSON.stringify(frame('delta'))}\n\n`,
        ),
      )
      .mockImplementation(() => new Promise<Response>(() => undefined));
    const onResync = vi.fn();
    const disconnect = connectActivityAccountEvents({
      onReplace: vi.fn(),
      onResync,
      fetchStream,
      random: () => 1,
    });
    await flushAsyncWork();
    window.dispatchEvent(new Event('focus'));
    window.dispatchEvent(new Event('focus'));
    await flushAsyncWork();
    expect(fetchStream).toHaveBeenCalledTimes(2);
    expect(onResync).toHaveBeenCalledWith('reconnect');
    expect(onResync).toHaveBeenCalledWith('malformed');
    expect(onResync.mock.calls.filter(([reason]) => reason === 'disconnect')).toHaveLength(2);
    disconnect();
  });
});
