import { act, renderHook, waitFor } from '@testing-library/react';
import { ApiError } from '@shared/query/http';
import { afterEach, describe, expect, it, vi } from 'vitest';
import * as api from './api';
import * as sse from './sseClient';
import { useDebugSession } from './useDebugSession';
import type { DebugEventEnvelope, DebugSessionMetadata } from './types';

vi.mock('./api', () => ({
  getDebugSession: vi.fn(),
  issueLiveChallenge: vi.fn(),
  setDebugMode: vi.fn(),
  startDebugSession: vi.fn(),
  stopDebugSession: vi.fn(),
}));

vi.mock('./sseClient', () => ({ connectDebugEvents: vi.fn() }));

const limits = {
  max_sessions: 64,
  hub_bytes: 128,
  session_bytes: 4,
  max_traces: 32,
  max_events: 128,
  event_bytes: 512,
  subscriber_queue: 64,
  max_subscribers: 2,
  raw_request_bytes: 64,
  messages_tools_bytes: 128,
  parameters_bytes: 64,
  effective_summary_bytes: 64,
  response_bytes: 256,
  trace_bytes: 768,
  first_attach_seconds: 30,
  reconnect_seconds: 30,
  idle_seconds: 600,
  absolute_seconds: 3_600,
  heartbeat_seconds: 15,
  write_deadline_seconds: 15,
  confirmation_seconds: 60,
} as const;

const session: DebugSessionMetadata = {
  id: 'dbg_abcdefghijklmnopqrstuv',
  generation: 1,
  mode: 'dry',
  created_at: 1,
  expires_at: 3_601,
  idle_expires_at: 601,
  connected: true,
  last_event_id: 0,
  limits,
};

const liveSession: DebugSessionMetadata = { ...session, mode: 'live' };

function connectedSnapshotEvent(): DebugEventEnvelope {
  return {
    version: 1,
    seq: 1,
    type: 'session_snapshot',
    session_id: session.id,
    trace_id: '',
    revision: 0,
    at: 1,
    payload: {
      metadata: { ...session, connected: true, last_event_id: 1 },
      traces: [],
    },
  };
}

describe('useDebugSession mode uncertainty', () => {
  afterEach(() => {
    vi.useRealTimers();
    vi.clearAllMocks();
  });

  it('detaches and reconnects when live mode response is lost after a possible server commit', async () => {
    vi.mocked(api.getDebugSession).mockResolvedValue(session);
    vi.mocked(api.issueLiveChallenge).mockResolvedValue('one-time-confirmation');
    let modeCalls = 0;
    vi.mocked(api.setDebugMode).mockImplementation(async (mode) => {
      modeCalls += 1;
      if (mode === 'live') throw new Error('response lost');
      return { ...session, mode: 'dry' };
    });
    let streams = 0;
    vi.mocked(sse.connectDebugEvents).mockImplementation(async ({ onEvent }) => {
      streams += 1;
      if (streams === 1) await onEvent(connectedSnapshotEvent());
      await new Promise<void>(() => undefined);
    });
    const { result } = renderHook(() => useDebugSession());
    await waitFor(() => expect(result.current.metadata?.id).toBe('dbg_abcdefghijklmnopqrstuv'));
    const firstSignal = vi.mocked(sse.connectDebugEvents).mock.calls[0]?.[0].signal;
    await result.current.enableLive();
    expect(result.current.mode).toBe('dry');
    expect(firstSignal?.aborted).toBe(true);
    await waitFor(
      () => expect(vi.mocked(sse.connectDebugEvents).mock.calls.length).toBeGreaterThan(1),
      { timeout: 1_000 },
    );
    expect(modeCalls).toBe(3);
    expect(vi.mocked(sse.connectDebugEvents).mock.calls[1]?.[0].lastEventId).toBe(
      Number.MAX_SAFE_INTEGER,
    );
  });

  it('takes the same detach path when a dry mode response is uncertain', async () => {
    vi.mocked(api.getDebugSession).mockResolvedValue(session);
    let modeCalls = 0;
    vi.mocked(api.setDebugMode).mockImplementation(async () => {
      modeCalls += 1;
      if (modeCalls === 2) throw new Error('response lost');
      return { ...session, mode: 'dry' };
    });
    vi.mocked(sse.connectDebugEvents).mockImplementation(async ({ onEvent }) => {
      await onEvent(connectedSnapshotEvent());
      await new Promise<void>(() => undefined);
    });
    const { result } = renderHook(() => useDebugSession());
    await waitFor(() => expect(result.current.metadata?.id).toBe('dbg_abcdefghijklmnopqrstuv'));
    const firstSignal = vi.mocked(sse.connectDebugEvents).mock.calls[0]?.[0].signal;
    await result.current.setDry();
    expect(result.current.mode).toBe('dry');
    expect(firstSignal?.aborted).toBe(true);
    await waitFor(
      () => expect(vi.mocked(sse.connectDebugEvents).mock.calls.length).toBeGreaterThan(1),
      { timeout: 1_000 },
    );
    expect(modeCalls).toBe(3);
  });

  it('does not reopen after an EOF until the authoritative dry write and readback finish', async () => {
    vi.mocked(api.getDebugSession).mockResolvedValue(session);
    let resolveDry!: () => void;
    const dryGate = new Promise<void>((resolve) => {
      resolveDry = resolve;
    });
    let modeCalls = 0;
    vi.mocked(api.setDebugMode).mockImplementation(async () => {
      modeCalls += 1;
      if (modeCalls > 1) await dryGate;
      return { ...session, mode: 'dry' };
    });
    let streams = 0;
    vi.mocked(sse.connectDebugEvents).mockImplementation(async () => {
      streams += 1;
      if (streams === 1) return;
      await new Promise<void>(() => undefined);
    });
    const { result } = renderHook(() => useDebugSession());
    await waitFor(() => expect(vi.mocked(sse.connectDebugEvents).mock.calls.length).toBe(1));
    await waitFor(() => expect(vi.mocked(api.setDebugMode).mock.calls.length).toBe(1), {
      timeout: 1_000,
    });
    expect(vi.mocked(sse.connectDebugEvents).mock.calls.length).toBe(1);
    resolveDry();
    await waitFor(() => expect(vi.mocked(sse.connectDebugEvents).mock.calls.length).toBe(2), {
      timeout: 1_000,
    });
    expect(result.current.mode).toBe('dry');
  });

  it.each([
    [404, 'not_found'],
    [401, 'unauthorized'],
  ] as const)(
    'terminates without looping when dry recovery returns HTTP %s',
    async (status, code) => {
      vi.mocked(api.getDebugSession).mockResolvedValue(session);
      let modeCalls = 0;
      vi.mocked(api.setDebugMode).mockImplementation(async () => {
        modeCalls += 1;
        if (modeCalls > 1)
          throw new ApiError(code, 'The debug session is no longer available.', status);
        return { ...session, mode: 'dry' };
      });
      vi.mocked(sse.connectDebugEvents).mockResolvedValue(undefined);
      const { result } = renderHook(() => useDebugSession());

      await waitFor(() => expect(vi.mocked(sse.connectDebugEvents).mock.calls).toHaveLength(1));
      await waitFor(() => expect(result.current.connection).toBe('expired'));

      expect(result.current.metadata).toBeNull();
      expect(result.current.endReason).toBe('session_invalid');
      expect(vi.mocked(sse.connectDebugEvents).mock.calls).toHaveLength(1);
    },
  );

  it('terminates without looping when the control plane returns an inactive session', async () => {
    let reads = 0;
    vi.mocked(api.getDebugSession).mockImplementation(async () => {
      reads += 1;
      return reads <= 2 ? session : { active: false };
    });
    vi.mocked(api.setDebugMode).mockResolvedValue({ ...session, mode: 'dry' });
    vi.mocked(sse.connectDebugEvents).mockResolvedValue(undefined);
    const { result } = renderHook(() => useDebugSession());

    await waitFor(() => expect(result.current.connection).toBe('expired'));
    expect(result.current.metadata).toBeNull();
    expect(result.current.endReason).toBe('session_invalid');
    expect(vi.mocked(api.getDebugSession).mock.calls.length).toBeGreaterThanOrEqual(2);
    expect(vi.mocked(sse.connectDebugEvents).mock.calls).toHaveLength(1);
  });

  it('does not let a late start response resurrect a session after stop', async () => {
    vi.mocked(api.getDebugSession).mockResolvedValue({ active: false });
    vi.mocked(api.stopDebugSession).mockResolvedValue(undefined);
    let resolveStart!: (value: DebugSessionMetadata) => void;
    const startGate = new Promise<DebugSessionMetadata>((resolve) => {
      resolveStart = resolve;
    });
    vi.mocked(api.startDebugSession).mockReturnValue(startGate);
    vi.mocked(sse.connectDebugEvents).mockResolvedValue(undefined);
    const { result } = renderHook(() => useDebugSession());

    await waitFor(() => expect(result.current.connection).toBe('stopped'));
    let startPromise!: Promise<void>;
    await act(async () => {
      startPromise = result.current.start();
      await Promise.resolve();
    });
    await waitFor(() => expect(api.startDebugSession).toHaveBeenCalledOnce());
    await act(async () => {
      await result.current.stop();
    });
    expect(result.current.connection).toBe('stopped');
    await act(async () => {
      resolveStart(session);
      await startPromise;
    });

    expect(result.current.connection).toBe('stopped');
    expect(result.current.metadata).toBeNull();
    expect(vi.mocked(sse.connectDebugEvents).mock.calls).toHaveLength(0);
  });

  it('does not send a live PUT when the challenge resolves after unmount', async () => {
    vi.mocked(api.getDebugSession).mockResolvedValue(session);
    vi.mocked(api.setDebugMode).mockImplementation(async (mode) =>
      mode === 'live' ? liveSession : session,
    );
    let resolveChallenge!: (value: string) => void;
    const challengeGate = new Promise<string>((resolve) => {
      resolveChallenge = resolve;
    });
    vi.mocked(api.issueLiveChallenge).mockReturnValue(challengeGate);
    vi.mocked(sse.connectDebugEvents).mockImplementation(async ({ onEvent }) => {
      await onEvent(connectedSnapshotEvent());
      await new Promise<void>(() => undefined);
    });
    const { result, unmount } = renderHook(() => useDebugSession());
    await waitFor(() => expect(result.current.metadata?.id).toBe(session.id));

    let enablePromise!: Promise<void>;
    await act(async () => {
      enablePromise = result.current.enableLive();
      await Promise.resolve();
    });
    unmount();
    resolveChallenge('one-time-confirmation');
    await act(async () => {
      await enablePromise;
    });

    expect(vi.mocked(api.setDebugMode).mock.calls.some(([mode]) => mode === 'live')).toBe(false);
  });

  it('does not send a stale live PUT after stop invalidates the operation', async () => {
    vi.mocked(api.getDebugSession).mockResolvedValue(session);
    vi.mocked(api.stopDebugSession).mockResolvedValue(undefined);
    vi.mocked(api.setDebugMode).mockImplementation(async (mode) =>
      mode === 'live' ? liveSession : session,
    );
    let resolveChallenge!: (value: string) => void;
    const challengeGate = new Promise<string>((resolve) => {
      resolveChallenge = resolve;
    });
    vi.mocked(api.issueLiveChallenge).mockReturnValue(challengeGate);
    vi.mocked(sse.connectDebugEvents).mockImplementation(async ({ onEvent }) => {
      await onEvent(connectedSnapshotEvent());
      await new Promise<void>(() => undefined);
    });
    const { result } = renderHook(() => useDebugSession());
    await waitFor(() => expect(result.current.metadata?.id).toBe(session.id));

    let enablePromise!: Promise<void>;
    await act(async () => {
      enablePromise = result.current.enableLive();
      await Promise.resolve();
    });
    await act(async () => {
      await result.current.stop();
    });
    resolveChallenge('one-time-confirmation');
    await act(async () => {
      await enablePromise;
    });

    expect(vi.mocked(api.setDebugMode).mock.calls.some(([mode]) => mode === 'live')).toBe(false);
  });

  it('forces an existing live session dry before binding the stream after refresh', async () => {
    let reads = 0;
    vi.mocked(api.getDebugSession).mockImplementation(async () => {
      reads += 1;
      return reads === 1 ? liveSession : session;
    });
    vi.mocked(api.setDebugMode).mockResolvedValue(session);
    vi.mocked(sse.connectDebugEvents).mockImplementation(() => new Promise<void>(() => undefined));
    const { result } = renderHook(() => useDebugSession());

    await waitFor(() => expect(result.current.metadata?.id).toBe('dbg_abcdefghijklmnopqrstuv'));
    expect(vi.mocked(api.setDebugMode).mock.calls).toEqual([['dry']]);
    expect(result.current.mode).toBe('dry');
    expect(vi.mocked(sse.connectDebugEvents).mock.calls).toHaveLength(1);
  });

  it('does not bind an existing session when refresh dry confirmation fails', async () => {
    vi.mocked(api.getDebugSession).mockResolvedValue(liveSession);
    vi.mocked(api.setDebugMode).mockRejectedValue(new Error('control plane unavailable'));
    const { result } = renderHook(() => useDebugSession());

    await waitFor(() => expect(result.current.connection).toBe('error'));
    expect(result.current.metadata).toBeNull();
    expect(result.current.mode).toBe('dry');
    expect(vi.mocked(sse.connectDebugEvents).mock.calls).toHaveLength(0);
  });

  it('backs off and eventually blocks a continuously closing stream', async () => {
    vi.useFakeTimers();
    vi.mocked(api.getDebugSession).mockResolvedValue(session);
    vi.mocked(api.setDebugMode).mockResolvedValue(session);
    vi.mocked(sse.connectDebugEvents).mockResolvedValue(undefined);
    const { result } = renderHook(() => useDebugSession());

    await act(async () => {
      await vi.runAllTicks();
      await vi.runAllTimersAsync();
    });
    expect(vi.mocked(sse.connectDebugEvents).mock.calls.length).toBe(6);
    expect(result.current.connection).toBe('error');
    expect(result.current.mode).toBe('dry');
  });

  it('bounds retries for a stream rate-limit response', async () => {
    vi.useFakeTimers();
    vi.mocked(api.getDebugSession).mockResolvedValue(session);
    vi.mocked(api.setDebugMode).mockResolvedValue(session);
    vi.mocked(sse.connectDebugEvents).mockRejectedValue(
      new ApiError('rate_limited', 'The debug stream is temporarily unavailable.', 429),
    );
    const { result } = renderHook(() => useDebugSession());

    await act(async () => {
      await vi.runAllTicks();
      await vi.runAllTimersAsync();
    });
    expect(vi.mocked(sse.connectDebugEvents).mock.calls.length).toBe(6);
    expect(result.current.connection).toBe('error');
    expect(result.current.mode).toBe('dry');
  });
});
