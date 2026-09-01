import { describe, expect, it } from 'vitest';
import { applyDebugEvent, initialDebugState } from './v2state';
import { normalizeDebugEvent, normalizeDebugSession, valuePresence } from './v2types';

const oid = (prefix: string, seed = 'A') => `${prefix}${seed.repeat(21)}A`;
const limits = { session_bytes: 4194304, traces: 32, events: 128, subscribers: 2, event_bytes: 524288, trace_bytes: 786432 };
const session = { active: true, id: oid('dbs_'), generation: '1', revision: '1', mode: 'dry', created_at: 1, expires_at: 3, idle_expires_at: 2, inflight_count: 0, connected_subscribers: 0, last_event_id: null, limits };

describe('Debug v2 wire', () => {
  it('keeps inactive metadata a closed one-field union', () => {
    expect(normalizeDebugSession({ active: false })).toEqual({ active: false });
    expect(() => normalizeDebugSession({ active: false, mode: 'dry' })).toThrow(/invalid/i);
  });

  it('distinguishes parameter presence without truthiness coercion', () => {
    expect(valuePresence(undefined, false)).toBe('missing');
    expect(valuePresence(null, true)).toBe('null');
    expect(valuePresence(false, true)).toBe('false');
    expect(valuePresence(0, true)).toBe('zero');
    expect(valuePresence('', true)).toBe('empty-string');
  });

  it('records a gap and replaces it only with the following snapshot', () => {
    const base = initialDebugState(normalizeDebugSession(session));
    const gap = normalizeDebugEvent({ version: 2, event_id: oid('dbe_'), session_id: session.id, generation: '1', kind: 'gap', occurred_at: 2, data: { reason: 'ring_evicted', first_available_event_id: null } });
    const afterGap = applyDebugEvent(base, gap);
    expect(afterGap.gap).toBe('ring_evicted');
    const snapshot = normalizeDebugEvent({ version: 2, event_id: oid('dbe_', 'B'), session_id: session.id, generation: '1', kind: 'snapshot', occurred_at: 2, data: { session, traces: [], first_event_id: null, last_event_id: null } });
    expect(applyDebugEvent(afterGap, snapshot).gap).toBeNull();
  });

  it('rejects raw upstream fields rather than ignoring them', () => {
    expect(() => normalizeDebugEvent({ version: 2, event_id: oid('dbe_'), session_id: session.id, generation: '1', kind: 'trace_upsert', occurred_at: 2, data: { raw_upstream_body: 'secret' } })).toThrow(/invalid/i);
  });

  it('requires the trace and request-body truncation markers to agree', () => {
    const wire = {
      version: 2,
      event_id: oid('dbe_'),
      session_id: session.id,
      generation: '1',
      kind: 'trace_upsert',
      occurred_at: 2,
      data: {
        trace_id: oid('dbt_'),
        revision: '1',
        state: 'capturing',
        request: {
          route_kind: 'openai_chat_completions',
          model: 'provider/model',
          stream: false,
          body: { media_type: 'application/json', byte_count: 2, text: '{}', base64: null, truncated: false },
        },
        upstream_result: null,
        caller_result: null,
        created_at: 1,
        updated_at: 2,
        truncated: false,
      },
    };
    const event = normalizeDebugEvent(wire);
    expect(event.kind).toBe('trace_upsert');
    if (event.kind === 'trace_upsert') {
      expect(event.data.truncated).toBe(false);
      expect(event.data.request.body.truncated).toBe(false);
    }
    expect(() => normalizeDebugEvent({ ...wire, data: { ...wire.data, truncated: true } })).toThrow(/truncation/i);
  });

  it('accepts only the fixed charity upstream projection', () => {
    const usage = {
      uncached_input_tokens: '0', cache_write_input_tokens: '0', cache_read_input_tokens: '0',
      output_tokens: '0', total_tokens: '0', usage_unknown: true, charge: '0',
    };
    const trace = {
      trace_id: oid('dbt_'), revision: '1', state: 'capturing',
      request: { route_kind: 'charity_chat_completions', model: 'charity/model', stream: false,
        body: { media_type: 'application/json', byte_count: 2, text: '{}', base64: null, truncated: false } },
      upstream_result: {
        result_kind: 'synthetic', status_code: 502, upstream_code: null, diag: null,
        usage, completed_at: 2,
      },
      caller_result: null, created_at: 1, updated_at: 2, truncated: false,
    };
    const envelope = { version: 2, event_id: oid('dbe_'), session_id: session.id, generation: '1', kind: 'trace_upsert', occurred_at: 2 };
    expect(normalizeDebugEvent({ ...envelope, data: trace }).kind).toBe('trace_upsert');
    expect(() => normalizeDebugEvent({
      ...envelope,
      data: {
        ...trace,
        upstream_result: {
          ...trace.upstream_result,
          result_kind: 'response', status_code: 429, upstream_code: 'rate_limit', diag: 'real upstream detail',
        },
      },
    })).toThrow(/charity Debug upstream projection/i);
  });

  it('rejects caller results whose status, source, and stable code disagree', () => {
    const trace = {
      trace_id: oid('dbt_'), revision: '1', state: 'terminal',
      request: { route_kind: 'openai_chat_completions', model: 'model', stream: false,
        body: { media_type: 'application/json', byte_count: 2, text: '{}', base64: null, truncated: false } },
      upstream_result: null, created_at: 1, updated_at: 2, truncated: false,
    };
    const envelope = { version: 2, event_id: oid('dbe_'), session_id: session.id, generation: '1', kind: 'trace_upsert', occurred_at: 2 };
    expect(() => normalizeDebugEvent({ ...envelope, data: { ...trace,
      caller_result: { http_status: 500, error_code: null, source: 'platform', message: '[NonbiriAPI] failed', completed_at: 2 },
    } })).toThrow(/status/i);
    expect(() => normalizeDebugEvent({ ...envelope, data: { ...trace,
      caller_result: { http_status: 502, error_code: 'upstream', source: 'platform', message: '[NonbiriAPI] failed', completed_at: 2 },
    } })).toThrow(/semantics/i);
    expect(() => normalizeDebugEvent({ ...envelope, data: { ...trace,
      caller_result: { http_status: 400, error_code: 'future_code', source: 'platform', message: '[NonbiriAPI] failed', completed_at: 2 },
    } })).toThrow(/code/i);
  });
});
