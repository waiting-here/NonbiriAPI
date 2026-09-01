import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '@shared/query/http';
import { applyDebugEvent, initialDebugState } from './v2state';
import { openDebugStream } from './v2stream';
import { normalizeDebugSession } from './v2types';

type Listener = EventListenerOrEventListenerObject;

class MockEventSource {
  static instances: MockEventSource[] = [];

  readonly url: string;
  readonly withCredentials: boolean;
  closed = false;
  onerror: ((event: Event) => void) | null = null;
  onopen: ((event: Event) => void) | null = null;
  private readonly listeners = new Map<string, Listener[]>();

  constructor(url: string | URL, init?: EventSourceInit) {
    this.url = String(url);
    this.withCredentials = init?.withCredentials ?? false;
    MockEventSource.instances.push(this);
  }

  addEventListener(type: string, listener: Listener | null): void {
    if (!listener) return;
    const listeners = this.listeners.get(type) ?? [];
    listeners.push(listener);
    this.listeners.set(type, listeners);
  }

  close(): void {
    this.closed = true;
  }

  open(): void {
    this.onopen?.(new Event('open'));
  }

  emit(type: string, data: string, lastEventId: string): void {
    const event = new MessageEvent<string>(type, { data, lastEventId });
    for (const listener of this.listeners.get(type) ?? []) {
      if (typeof listener === 'function') listener(event);
      else listener.handleEvent(event);
    }
  }
}

const NOW = 1_800_000_000;
const oid = (prefix: string, seed = 'A') => `${prefix}${seed.repeat(21)}A`;
const SESSION_ID = oid('dbs_');
const EVENT_A = oid('dbe_');
const EVENT_B = oid('dbe_', 'B');
const LIMITS = {
  session_bytes: 4_194_304,
  traces: 32,
  events: 128,
  subscribers: 2,
  event_bytes: 524_288,
  trace_bytes: 786_432,
};

function sessionWire(lastEventId: string | null = null) {
  return {
    active: true,
    id: SESSION_ID,
    generation: '5',
    revision: '9',
    mode: 'dry',
    created_at: NOW,
    expires_at: NOW + 3_600,
    idle_expires_at: NOW + 600,
    inflight_count: 0,
    connected_subscribers: 1,
    last_event_id: lastEventId,
    limits: LIMITS,
  };
}

function gapWire() {
  return {
    version: 2,
    event_id: EVENT_A,
    session_id: SESSION_ID,
    generation: '5',
    kind: 'gap',
    occurred_at: NOW + 1,
    data: { reason: 'ring_evicted', first_available_event_id: EVENT_A },
  };
}

function snapshotWire() {
  return {
    version: 2,
    event_id: EVENT_B,
    session_id: SESSION_ID,
    generation: '5',
    kind: 'snapshot',
    occurred_at: NOW + 2,
    data: {
      session: sessionWire(EVENT_B),
      traces: [],
      first_event_id: EVENT_A,
      last_event_id: EVENT_B,
    },
  };
}

function currentSource(): MockEventSource {
  const source = MockEventSource.instances.at(-1);
  if (!source) throw new Error('Expected a Debug EventSource instance.');
  return source;
}

beforeEach(() => {
  MockEventSource.instances = [];
  vi.stubGlobal('EventSource', MockEventSource as unknown as typeof EventSource);
});

describe('Debug v2 EventSource adapter', () => {
  it('fails closed when the SSE id and envelope event_id disagree', () => {
    const onEvent = vi.fn();
    const onStatus = vi.fn();
    const onError = vi.fn();
    openDebugStream(onEvent, onStatus, onError);
    const source = currentSource();

    source.emit('gap', JSON.stringify(gapWire()), EVENT_B);

    expect(source.closed).toBe(true);
    expect(onEvent).not.toHaveBeenCalled();
    expect(onStatus.mock.calls.map(([status]) => status)).toEqual(['connecting', 'disconnected']);
    expect(onError).toHaveBeenCalledTimes(1);
    expect(onError.mock.calls[0]?.[0]).toBeInstanceOf(ApiError);
    expect(onError.mock.calls[0]?.[0]).toMatchObject({ code: 'invalid_response', status: 200 });
  });

  it('fails closed when a named Debug event has a malformed envelope', () => {
    const onEvent = vi.fn();
    const onStatus = vi.fn();
    const onError = vi.fn();
    openDebugStream(onEvent, onStatus, onError);
    const source = currentSource();

    source.emit('snapshot', JSON.stringify({ version: 2, event_id: EVENT_A }), EVENT_A);

    expect(source.closed).toBe(true);
    expect(onEvent).not.toHaveBeenCalled();
    expect(onStatus.mock.calls.map(([status]) => status)).toEqual(['connecting', 'disconnected']);
    expect(onError).toHaveBeenCalledTimes(1);
    expect(onError.mock.calls[0]?.[0]).toBeInstanceOf(ApiError);
    expect(onError.mock.calls[0]?.[0]).toMatchObject({ code: 'invalid_response', status: 200 });
  });

  it('accepts a valid gap followed by its authoritative snapshot', () => {
    let state = initialDebugState(normalizeDebugSession(sessionWire()));
    const observedKinds: string[] = [];
    const onStatus = vi.fn();
    const onError = vi.fn();
    openDebugStream((event) => {
      observedKinds.push(event.kind);
      state = applyDebugEvent(state, event);
    }, onStatus, onError);
    const source = currentSource();

    expect(source.url).toBe('/api/debug/events');
    expect(source.withCredentials).toBe(true);
    source.open();
    source.emit('gap', JSON.stringify(gapWire()), EVENT_A);
    expect(state.gap).toBe('ring_evicted');

    source.emit('snapshot', JSON.stringify(snapshotWire()), EVENT_B);

    expect(observedKinds).toEqual(['gap', 'snapshot']);
    expect(state.gap).toBeNull();
    expect(state.session).toEqual(normalizeDebugSession(sessionWire(EVENT_B)));
    expect(source.closed).toBe(false);
    expect(onError).not.toHaveBeenCalled();
    expect(onStatus.mock.calls.map(([status]) => status)).toEqual(['connecting', 'connected']);
  });
});
