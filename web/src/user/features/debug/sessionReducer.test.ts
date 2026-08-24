import { describe, expect, it } from 'vitest';
import {
  debugSessionReducer,
  effectiveDebugMode,
  INITIAL_DEBUG_SESSION_STATE,
} from './sessionReducer';
import type { DebugSessionMetadata } from './types';

const metadata: DebugSessionMetadata = {
  id: 'dbg_abcdefghijklmnopqrstuv',
  generation: 1,
  mode: 'dry',
  created_at: 1,
  expires_at: 3_601,
  idle_expires_at: 601,
  connected: false,
  last_event_id: 0,
  limits: {
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
  },
};

describe('debug session reducer', () => {
  it('always starts and reconnects in dry mode', () => {
    const started = debugSessionReducer(INITIAL_DEBUG_SESSION_STATE, {
      type: 'session_received',
      metadata: { ...metadata, mode: 'live' },
    });
    expect(started.connection).toBe('connecting');
    expect(started.metadata?.mode).toBe('dry');
    const live = debugSessionReducer(
      {
        ...started,
        metadata: { ...started.metadata!, mode: 'live', connected: true },
        connection: 'connected',
      },
      { type: 'connect_requested', reconnect: true },
    );
    expect(live.connection).toBe('reconnecting');
    expect(effectiveDebugMode(live)).toBe('dry');
  });

  it('maps terminal reasons to visible replaced/stopped/expired states', () => {
    const connected = debugSessionReducer(
      debugSessionReducer(INITIAL_DEBUG_SESSION_STATE, { type: 'session_received', metadata }),
      { type: 'connected' },
    );
    expect(
      debugSessionReducer(connected, { type: 'session_end', reason: 'replaced' }).connection,
    ).toBe('replaced');
    expect(
      debugSessionReducer(connected, { type: 'session_end', reason: 'stopped' }).connection,
    ).toBe('stopped');
    expect(
      debugSessionReducer(connected, { type: 'session_end', reason: 'idle_timeout' }).connection,
    ).toBe('expired');
  });
});
