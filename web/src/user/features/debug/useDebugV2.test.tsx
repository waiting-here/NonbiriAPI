import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, renderHook, waitFor } from '@testing-library/react';
import type { ReactNode } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '@shared/query/http';
import { useDebugV2 } from './useDebugV2';
import type { DebugObserverStatus } from './v2stream';
import type { DebugEvent, DebugSessionMetadata, DebugTrace } from './v2types';

const streamMocks = vi.hoisted(() => ({
  openDebugStream: vi.fn<typeof import('./v2stream').openDebugStream>(),
}));

vi.mock('./v2stream', () => ({
  openDebugStream: streamMocks.openDebugStream,
}));

type ActiveSession = Extract<DebugSessionMetadata, { active: true }>;

interface ControlledStream {
  close: ReturnType<typeof vi.fn>;
  onError: (error: unknown) => void;
  onEvent: (event: DebugEvent) => void;
  onStatus: (status: DebugObserverStatus) => void;
}

const NOW = 1_800_000_000;
const oid = (prefix: string, seed = 'A') => `${prefix}${seed.repeat(21)}A`;
const SESSION_ID = oid('dbs_');
const SNAPSHOT_EVENT_ID = oid('dbe_', 'B');
const LIMITS = {
  session_bytes: 4_194_304,
  traces: 32,
  events: 128,
  subscribers: 2,
  event_bytes: 524_288,
  trace_bytes: 786_432,
};
const SESSION = {
  active: true,
  id: SESSION_ID,
  generation: '3',
  revision: '7',
  mode: 'dry',
  created_at: NOW,
  expires_at: NOW + 3_600,
  idle_expires_at: NOW + 600,
  inflight_count: 0,
  connected_subscribers: 0,
  last_event_id: null,
  limits: LIMITS,
} satisfies ActiveSession;
const TRACE = {
  trace_id: oid('dbt_'),
  revision: '1',
  state: 'capturing',
  request: {
    route_kind: 'openai_chat_completions',
    model: 'owner/model',
    stream: false,
    body: {
      media_type: 'application/json',
      byte_count: 2,
      text: '{}',
      base64: null,
      truncated: false,
    },
  },
  upstream_result: null,
  caller_result: null,
  created_at: NOW + 1,
  updated_at: NOW + 1,
  truncated: false,
} satisfies DebugTrace;

let controlledStreams: ControlledStream[] = [];
const queryClients = new Set<QueryClient>();

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

function errorResponse(status: 401 | 403 | 409): Response {
  const code = status === 401 ? 'unauthorized' : status === 403 ? 'forbidden' : 'conflict';
  return jsonResponse({ error: { code, message: `safe ${code}` } }, status);
}

function snapshotEvent(): DebugEvent {
  const snapshotSession = { ...SESSION, last_event_id: SNAPSHOT_EVENT_ID };
  return {
    version: 2,
    event_id: SNAPSHOT_EVENT_ID,
    session_id: SESSION_ID,
    generation: SESSION.generation,
    kind: 'snapshot',
    occurred_at: NOW + 2,
    data: {
      session: snapshotSession,
      traces: [TRACE],
      first_event_id: SNAPSHOT_EVENT_ID,
      last_event_id: SNAPSHOT_EVENT_ID,
    },
  };
}

function renderDebugHook() {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: Number.POSITIVE_INFINITY },
    },
  });
  queryClients.add(queryClient);
  function Wrapper({ children }: { children: ReactNode }) {
    return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
  }
  return { queryClient, ...renderHook(() => useDebugV2(), { wrapper: Wrapper }) };
}

function requestSequence(fetchMock: ReturnType<typeof vi.fn<typeof fetch>>): string[] {
  return fetchMock.mock.calls.map(
    ([input, init]) => `${init?.method ?? 'GET'} ${String(input)}`,
  );
}

beforeEach(() => {
  controlledStreams = [];
  streamMocks.openDebugStream.mockReset();
  streamMocks.openDebugStream.mockImplementation((onEvent, onStatus, onError) => {
    const controlled = { close: vi.fn(), onError, onEvent, onStatus };
    controlledStreams.push(controlled);
    onStatus('connecting');
    return { close: controlled.close };
  });
});

afterEach(() => {
  for (const queryClient of queryClients) queryClient.clear();
  queryClients.clear();
});

describe('useDebugV2 authority and observer boundaries', () => {
  it('loads the authoritative active session before attaching its SSE observer', async () => {
    const fetchMock = vi.fn<typeof fetch>(async () => jsonResponse(SESSION));
    vi.stubGlobal('fetch', fetchMock);

    const rendered = renderDebugHook();
    await waitFor(() => expect(rendered.result.current.view.session).toEqual(SESSION));
    await waitFor(() => expect(streamMocks.openDebugStream).toHaveBeenCalledTimes(1));

    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(fetchMock.mock.invocationCallOrder[0]).toBeLessThan(
      streamMocks.openDebugStream.mock.invocationCallOrder[0] ?? Number.POSITIVE_INFINITY,
    );
    expect(requestSequence(fetchMock)).toEqual(['GET /api/debug/session']);
    expect(controlledStreams).toHaveLength(1);

    act(() => controlledStreams[0]?.onStatus('connected'));
    expect(rendered.result.current.observer).toBe('connected');
  });

  it('disconnects only the observer and never calls the stop mutation', async () => {
    const fetchMock = vi.fn<typeof fetch>(async () => jsonResponse(SESSION));
    vi.stubGlobal('fetch', fetchMock);
    const rendered = renderDebugHook();
    await waitFor(() => expect(controlledStreams).toHaveLength(1));
    await waitFor(() => expect(rendered.result.current.view.session).toEqual(SESSION));

    act(() => {
      controlledStreams[0]?.onStatus('connected');
      controlledStreams[0]?.onEvent(snapshotEvent());
    });
    expect(rendered.result.current.view.traces).toEqual([TRACE]);

    act(() => rendered.result.current.disconnect());

    expect(controlledStreams[0]?.close).toHaveBeenCalledTimes(1);
    expect(rendered.result.current.view.session.active).toBe(true);
    expect(rendered.result.current.view.traces).toEqual([TRACE]);
    expect(
      fetchMock.mock.calls.filter(
        ([input, init]) => String(input) === '/api/debug/session/stop' && init?.method === 'POST',
      ),
    ).toHaveLength(0);
  });

  it('drops a terminal stream handle and reconnects only after an explicit retry', async () => {
    const fetchMock = vi.fn<typeof fetch>(async () => jsonResponse(SESSION));
    vi.stubGlobal('fetch', fetchMock);
    const rendered = renderDebugHook();
    await waitFor(() => expect(controlledStreams).toHaveLength(1));
    await waitFor(() => expect(rendered.result.current.view.session).toEqual(SESSION));

    const terminalError = new ApiError('invalid_response', 'Malformed Debug event.', 200);
    act(() => controlledStreams[0]?.onError(terminalError));

    expect(controlledStreams[0]?.close).toHaveBeenCalledTimes(1);
    expect(rendered.result.current.observer).toBe('disconnected');
    expect(rendered.result.current.streamError).toBe(terminalError);
    expect(streamMocks.openDebugStream).toHaveBeenCalledTimes(1);

    act(() => rendered.result.current.connect());
    await waitFor(() => expect(controlledStreams).toHaveLength(2));
    expect(rendered.result.current.streamError).toBeNull();
  });

  it('keeps one Idempotency-Key for an explicit retry after an unknown network result', async () => {
    const liveSession = { ...SESSION, revision: '8', mode: 'live' } satisfies ActiveSession;
    let modeMutations = 0;
    const fetchMock = vi.fn<typeof fetch>(async (input, init) => {
      const path = String(input);
      const method = init?.method ?? 'GET';
      if (path === '/api/debug/session' && method === 'GET') return jsonResponse(SESSION);
      if (path === '/api/debug/session/mode' && method === 'PUT') {
        modeMutations += 1;
        if (modeMutations === 1) throw new TypeError('response lost');
        return jsonResponse(liveSession);
      }
      throw new Error(`Unexpected request: ${method} ${path}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const rendered = renderDebugHook();
    await waitFor(() => expect(rendered.result.current.view.session).toEqual(SESSION));

    await act(async () => rendered.result.current.setMode('live', SESSION.revision));
    await waitFor(() => expect(rendered.result.current.mutating).toBe(false));
    expect(rendered.result.current.mutationError).toMatchObject({ status: 0 });
    expect(modeMutations).toBe(1);
    expect(requestSequence(fetchMock)).toEqual([
      'GET /api/debug/session',
      'PUT /api/debug/session/mode',
      'GET /api/debug/session',
    ]);

    await act(async () => rendered.result.current.setMode('live', SESSION.revision));
    await waitFor(() => expect(rendered.result.current.view.session).toEqual(liveSession));

    const mutations = fetchMock.mock.calls.filter(
      ([input, init]) => String(input) === '/api/debug/session/mode' && init?.method === 'PUT',
    );
    expect(mutations).toHaveLength(2);
    const firstKey = new Headers(mutations[0]?.[1]?.headers).get('Idempotency-Key');
    const secondKey = new Headers(mutations[1]?.[1]?.headers).get('Idempotency-Key');
    expect(firstKey).toMatch(/^[A-Za-z0-9_-]{22}$/);
    expect(secondKey).toBe(firstKey);
    expect(mutations.map(([, init]) => init?.body)).toEqual([
      JSON.stringify({ mode: 'live', expected_revision: '7', live_confirmation: true }),
      JSON.stringify({ mode: 'live', expected_revision: '7', live_confirmation: true }),
    ]);
    expect(window.localStorage).toHaveLength(0);
    expect(window.sessionStorage).toHaveLength(0);
  });

  it('handles 409 by performing one authoritative GET without resubmitting the mutation', async () => {
    const refreshedSession = { ...SESSION, revision: '8' } satisfies ActiveSession;
    let sessionReads = 0;
    const fetchMock = vi.fn<typeof fetch>(async (input, init) => {
      const path = String(input);
      const method = init?.method ?? 'GET';
      if (path === '/api/debug/session' && method === 'GET') {
        sessionReads += 1;
        return jsonResponse(sessionReads === 1 ? SESSION : refreshedSession);
      }
      if (path === '/api/debug/session/mode' && method === 'PUT') return errorResponse(409);
      throw new Error(`Unexpected request: ${method} ${path}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const rendered = renderDebugHook();
    await waitFor(() => expect(rendered.result.current.view.session).toEqual(SESSION));

    await act(async () => rendered.result.current.setMode('live', SESSION.revision));
    await waitFor(() => expect(rendered.result.current.view.session).toEqual(refreshedSession));

    expect(rendered.result.current.mutationError).toBeInstanceOf(ApiError);
    expect(rendered.result.current.mutationError).toMatchObject({ status: 409 });
    expect(requestSequence(fetchMock)).toEqual([
      'GET /api/debug/session',
      'PUT /api/debug/session/mode',
      'GET /api/debug/session',
    ]);
    expect(
      fetchMock.mock.calls.filter(
        ([input, init]) => String(input) === '/api/debug/session/mode' && init?.method === 'PUT',
      ),
    ).toHaveLength(1);
  });

  it.each([401, 403] as const)(
    'closes the stream and erases captured traces after an authoritative %s',
    async (status) => {
      let authorityFailure: 401 | 403 | null = null;
      const fetchMock = vi.fn<typeof fetch>(async (input, init) => {
        const path = String(input);
        const method = init?.method ?? 'GET';
        if (path === '/api/debug/session' && method === 'GET') {
          return authorityFailure === null ? jsonResponse(SESSION) : errorResponse(authorityFailure);
        }
        throw new Error(`Unexpected request: ${method} ${path}`);
      });
      vi.stubGlobal('fetch', fetchMock);
      const rendered = renderDebugHook();
      await waitFor(() => expect(controlledStreams).toHaveLength(1));
      await waitFor(() => expect(rendered.result.current.view.session).toEqual(SESSION));

      act(() => controlledStreams[0]?.onEvent(snapshotEvent()));
      expect(rendered.result.current.view.traces).toEqual([TRACE]);
      authorityFailure = status;

      await act(async () => {
        await rendered.result.current.authority.refetch();
      });
      await waitFor(() => expect(rendered.result.current.view.session).toEqual({ active: false }));

      expect(controlledStreams[0]?.close).toHaveBeenCalledTimes(1);
      expect(rendered.result.current.view.traces).toEqual([]);
      expect(rendered.result.current.observer).toBe('disconnected');
      expect(rendered.result.current.streamError).toBeNull();
      expect(window.localStorage).toHaveLength(0);
      expect(window.sessionStorage).toHaveLength(0);
    },
  );
});
