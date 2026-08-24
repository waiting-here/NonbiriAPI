import { describe, expect, it, vi } from 'vitest';
import { DebugEventStore } from './eventStore';
import { decodeEventEnvelope, decodeSessionMetadata } from './types';
import type { DebugEventEnvelope, DebugLimits, DebugSessionMetadata } from './types';

const limits: DebugLimits = {
  max_sessions: 64,
  hub_bytes: 128 * 1024 * 1024,
  session_bytes: 4 * 1024 * 1024,
  max_traces: 32,
  max_events: 128,
  event_bytes: 512 * 1024,
  subscriber_queue: 64,
  max_subscribers: 2,
  raw_request_bytes: 64 * 1024,
  messages_tools_bytes: 128 * 1024,
  parameters_bytes: 64 * 1024,
  effective_summary_bytes: 64 * 1024,
  response_bytes: 256 * 1024,
  trace_bytes: 768 * 1024,
  first_attach_seconds: 30,
  reconnect_seconds: 30,
  idle_seconds: 600,
  absolute_seconds: 3600,
  heartbeat_seconds: 15,
  write_deadline_seconds: 15,
  confirmation_seconds: 60,
};

const metadata = (overrides: Partial<DebugSessionMetadata> = {}): DebugSessionMetadata => ({
  id: 'dbg_abcdefghijklmnopqrstuv',
  generation: 1,
  mode: 'dry',
  created_at: 1_700_000_000,
  expires_at: 1_700_003_600,
  idle_expires_at: 1_700_000_600,
  connected: true,
  last_event_id: 1,
  limits,
  ...overrides,
});

function envelope(
  type: DebugEventEnvelope['type'],
  seq: number,
  payload: Record<string, unknown>,
  overrides: Partial<DebugEventEnvelope> = {},
): DebugEventEnvelope {
  return {
    version: 1,
    seq,
    type,
    session_id: 'dbg_abcdefghijklmnopqrstuv',
    trace_id: '',
    revision: 0,
    at: 1_700_000_000 + seq,
    payload,
    ...overrides,
  };
}

function base64url(value: Uint8Array): string {
  let binary = '';
  for (const byte of value) binary += String.fromCharCode(byte);
  return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

async function fragmentParts(
  value: string,
  count: number,
): Promise<{ data: string; sha256: string }[]> {
  const bytes = new TextEncoder().encode(value);
  const digest = await crypto.subtle.digest('SHA-256', bytes);
  const sha256 = Array.from(new Uint8Array(digest), (byte) =>
    byte.toString(16).padStart(2, '0'),
  ).join('');
  const parts: { data: string; sha256: string }[] = [];
  for (let index = 0; index < count; index += 1) {
    const start = Math.floor((bytes.length * index) / count);
    const end = Math.floor((bytes.length * (index + 1)) / count);
    parts.push({ data: base64url(bytes.slice(start, end)), sha256 });
  }
  return parts;
}

describe('DebugEventStore', () => {
  it('requires the bound session and rejects an old session without applying its trace', async () => {
    const store = new DebugEventStore();
    store.bindSession('dbg_abcdefghijklmnopqrstuv');
    await store.apply(envelope('session_snapshot', 1, { metadata: metadata(), traces: [] }));
    const old = await store.apply({
      ...envelope(
        'trace_upsert',
        2,
        { trace: { request_id: 'old', terminal: 'completed' } },
        { trace_id: 'old', revision: 1 },
      ),
      session_id: 'dbg_1234567890123456789012',
    });
    expect(old.recoveryRequired).toBe(true);
    expect(old.gapReason).toBe('session_mismatch');
    expect(old.traces).toHaveLength(0);
    const future = await store.apply(
      envelope(
        'trace_upsert',
        3,
        { trace: { request_id: 'future' } },
        { trace_id: 'future', revision: 1 },
      ),
    );
    expect(future.traces).toHaveLength(0);
    expect(future.recoveryRequired).toBe(true);
  });

  it('rejects non-monotonic trace revisions and accepts them only after a fresh snapshot', async () => {
    const store = new DebugEventStore('dbg_abcdefghijklmnopqrstuv');
    await store.apply(envelope('session_snapshot', 1, { metadata: metadata(), traces: [] }));
    await store.apply(
      envelope(
        'trace_upsert',
        2,
        { trace: { request_id: 'trace-1', mode: 'dry', terminal: 'received' } },
        { trace_id: 'trace-1', revision: 1 },
      ),
    );
    const invalid = await store.apply(
      envelope(
        'trace_upsert',
        3,
        { trace: { request_id: 'trace-1', mode: 'dry', terminal: 'failed' } },
        { trace_id: 'trace-1', revision: 1 },
      ),
    );
    expect(invalid.recoveryRequired).toBe(true);
    expect(invalid.traces).toHaveLength(0);
    const recovered = await store.apply(
      envelope('session_snapshot', 4, { metadata: metadata({ last_event_id: 4 }), traces: [] }),
    );
    expect(recovered.recoveryRequired).toBe(false);
    expect(recovered.traces).toHaveLength(0);
    const next = await store.apply(
      envelope(
        'trace_upsert',
        5,
        { trace: { request_id: 'trace-1', mode: 'dry', terminal: 'completed' } },
        { trace_id: 'trace-1', revision: 2 },
      ),
    );
    expect(next.recoveryRequired).toBe(false);
    expect(next.traces[0]?.payload.terminal).toBe('completed');
  });

  it('requires type-specific envelope identity and trace request correlation', async () => {
    const snapshot = envelope('session_snapshot', 1, { metadata: {}, traces: [] });
    expect(decodeEventEnvelope({ ...snapshot, trace_id: 'unexpected' })).toBeNull();
    expect(decodeEventEnvelope({ ...snapshot, revision: 1 })).toBeNull();
    expect(
      decodeEventEnvelope(
        envelope(
          'trace_upsert',
          1,
          { trace: { request_id: 'trace-1' } },
          { trace_id: '', revision: 1 },
        ),
      ),
    ).toBeNull();

    const store = new DebugEventStore('dbg_abcdefghijklmnopqrstuv');
    await store.apply(envelope('session_snapshot', 1, { metadata: metadata(), traces: [] }));
    const mismatch = await store.apply(
      envelope(
        'trace_upsert',
        2,
        { trace: { request_id: 'different-trace' } },
        { trace_id: 'trace-1', revision: 1 },
      ),
    );
    expect(mismatch.recoveryRequired).toBe(true);
    expect(mismatch.recoveryFatal).toBe(true);
    expect(mismatch.traces).toHaveLength(0);
  });

  it('allowlists limits and rejects inverted session expiry metadata', () => {
    const decoded = decodeSessionMetadata({
      ...metadata(),
      limits: { ...limits, leaked_field: 'secret' },
    });
    expect(decoded).not.toBeNull();
    expect(decoded?.limits).not.toHaveProperty('leaked_field');
    expect(
      decodeSessionMetadata({ ...metadata(), idle_expires_at: metadata().expires_at + 1 }),
    ).toBeNull();
    expect(
      decodeSessionMetadata({ ...metadata(), expires_at: metadata().created_at - 1 }),
    ).toBeNull();
  });

  it('rejects unknown trace mode or terminal states instead of displaying dry/received', async () => {
    for (const trace of [
      { request_id: 'trace-1', mode: 'mystery', terminal: 'received' },
      { request_id: 'trace-1', mode: 'dry', terminal: 'mystery' },
    ]) {
      const store = new DebugEventStore('dbg_abcdefghijklmnopqrstuv');
      await store.apply(envelope('session_snapshot', 1, { metadata: metadata(), traces: [] }));
      const invalid = await store.apply(
        envelope('trace_upsert', 2, { trace }, { trace_id: 'trace-1', revision: 1 }),
      );
      expect(invalid.recoveryFatal).toBe(true);
      expect(invalid.traces).toHaveLength(0);
    }
  });

  it('clears the page ring on gap/end and keeps safe lifecycle metadata', async () => {
    const store = new DebugEventStore('dbg_abcdefghijklmnopqrstuv');
    await store.apply(
      envelope('session_snapshot', 1, {
        metadata: metadata(),
        traces: [{ request_id: 'r', mode: 'dry', terminal: 'received' }],
      }),
    );
    const gap = await store.apply(envelope('gap', 2, { reason: 'resume_gap', dropped: 2 }));
    expect(gap.traces).toHaveLength(0);
    expect(gap.gapReason).toBe('resume_gap');
    expect(gap.recoveryRequired).toBe(true);
    expect(gap.recoveryFatal).toBe(false);
    const recovered = await store.apply(
      envelope('session_snapshot', 3, { metadata: metadata({ last_event_id: 3 }), traces: [] }),
    );
    expect(recovered.recoveryRequired).toBe(false);
    const end = await store.apply(envelope('session_end', 4, { reason: 'replaced' }));
    expect(end.endReason).toBe('replaced');
    expect(end.metadata).toMatchObject({ mode: 'dry', connected: false });
  });

  it('rejects a malformed dropped counter instead of normalizing it to zero', async () => {
    const store = new DebugEventStore('dbg_abcdefghijklmnopqrstuv');
    await store.apply(envelope('session_snapshot', 1, { metadata: metadata(), traces: [] }));
    const invalid = await store.apply(envelope('gap', 2, { reason: 'resume_gap', dropped: 'two' }));
    expect(invalid.gapReason).toBe('invalid_dropped');
    expect(invalid.recoveryFatal).toBe(true);
    expect(invalid.lastEventId).toBe(2);
  });

  it('waits through a gap and multi-part snapshot before accepting a trace delta', async () => {
    const store = new DebugEventStore('dbg_abcdefghijklmnopqrstuv');
    await store.apply(envelope('session_snapshot', 1, { metadata: metadata(), traces: [] }));
    const gap = await store.apply(envelope('gap', 2, { reason: 'resume_gap' }));
    expect(gap.recoveryRequired).toBe(true);
    expect(gap.recoveryFatal).toBe(false);

    const document = JSON.stringify({
      metadata: metadata({ last_event_id: 4 }),
      traces: [{ request_id: 'fragmented-request', mode: 'dry', terminal: 'received' }],
    });
    const parts = await fragmentParts(document, 2);
    const first = await store.apply(
      envelope('session_snapshot', 3, {
        fragment: {
          kind: 'snapshot',
          encoding: 'base64url-json',
          index: 0,
          count: 2,
          total_bytes: new TextEncoder().encode(document).byteLength,
          sha256: parts[0]?.sha256,
          data: parts[0]?.data,
        },
      }),
    );
    expect(first.recoveryRequired).toBe(true);
    expect(first.recoveryFatal).toBe(false);
    const complete = await store.apply(
      envelope('session_snapshot', 4, {
        fragment: {
          kind: 'snapshot',
          encoding: 'base64url-json',
          index: 1,
          count: 2,
          total_bytes: new TextEncoder().encode(document).byteLength,
          sha256: parts[1]?.sha256,
          data: parts[1]?.data,
        },
      }),
    );
    expect(complete.recoveryRequired).toBe(false);
    expect(complete.recoveryFatal).toBe(false);
    expect(complete.traces[0]?.id).toBe('fragmented-request');
  });

  it('rejects cumulative fragment bytes before retaining more than the declared total', async () => {
    const store = new DebugEventStore('dbg_abcdefghijklmnopqrstuv');
    const hash = '0'.repeat(64);
    const first = await store.apply(
      envelope('session_snapshot', 1, {
        fragment: {
          kind: 'snapshot',
          encoding: 'base64url-json',
          index: 0,
          count: 2,
          total_bytes: 3,
          sha256: hash,
          data: base64url(new Uint8Array([1, 2])),
        },
      }),
    );
    expect(first.recoveryFatal).toBe(false);

    const rejected = await store.apply(
      envelope('session_snapshot', 2, {
        fragment: {
          kind: 'snapshot',
          encoding: 'base64url-json',
          index: 1,
          count: 2,
          total_bytes: 3,
          sha256: hash,
          data: base64url(new Uint8Array([3, 4])),
        },
      }),
    );
    expect(rejected.recoveryRequired).toBe(true);
    expect(rejected.recoveryFatal).toBe(true);
    expect(rejected.gapReason).toBe('invalid_fragment_total');
  });

  it('rejects a non-snapshot event after a gap without advancing its cursor', async () => {
    const store = new DebugEventStore('dbg_abcdefghijklmnopqrstuv');
    await store.apply(envelope('session_snapshot', 1, { metadata: metadata(), traces: [] }));
    await store.apply(envelope('gap', 2, { reason: 'resume_gap' }));
    const blocked = await store.apply(
      envelope(
        'trace_upsert',
        3,
        { trace: { request_id: 'future' } },
        { trace_id: 'future', revision: 1 },
      ),
    );
    expect(blocked.recoveryRequired).toBe(true);
    expect(blocked.recoveryFatal).toBe(true);
    expect(blocked.lastEventId).toBe(3);
  });

  it.each([
    [
      'uppercase hash',
      (parts: { data: string; sha256: string }[]) => parts[0]!.sha256.toUpperCase(),
    ],
    ['hash mismatch', () => '0'.repeat(64)],
  ])('rejects %s in a complete snapshot fragment', async (_label, hash) => {
    const store = new DebugEventStore('dbg_abcdefghijklmnopqrstuv');
    const document = JSON.stringify({ metadata: metadata({ last_event_id: 1 }), traces: [] });
    const parts = await fragmentParts(document, 1);
    const result = await store.apply(
      envelope('session_snapshot', 1, {
        fragment: {
          kind: 'snapshot',
          encoding: 'base64url-json',
          index: 0,
          count: 1,
          total_bytes: new TextEncoder().encode(document).byteLength,
          sha256: hash(parts),
          data: parts[0]?.data,
        },
      }),
    );
    expect(result.recoveryRequired).toBe(true);
    expect(result.recoveryFatal).toBe(true);
  });

  it('rejects out-of-order, duplicate, and non-contiguous revision fragments', async () => {
    const document = JSON.stringify({ metadata: metadata({ last_event_id: 2 }), traces: [] });
    const parts = await fragmentParts(document, 2);
    const outOfOrder = new DebugEventStore('dbg_abcdefghijklmnopqrstuv');
    const invalidOrder = await outOfOrder.apply(
      envelope('session_snapshot', 1, {
        fragment: {
          kind: 'snapshot',
          encoding: 'base64url-json',
          index: 1,
          count: 2,
          total_bytes: new TextEncoder().encode(document).byteLength,
          sha256: parts[0]?.sha256,
          data: parts[1]?.data,
        },
      }),
    );
    expect(invalidOrder.gapReason).toBe('non_contiguous_fragment_index');

    const duplicate = new DebugEventStore('dbg_abcdefghijklmnopqrstuv');
    const first = {
      kind: 'snapshot' as const,
      encoding: 'base64url-json' as const,
      index: 0,
      count: 2,
      total_bytes: new TextEncoder().encode(document).byteLength,
      sha256: parts[0]?.sha256,
      data: parts[0]?.data,
    };
    await duplicate.apply(envelope('session_snapshot', 1, { fragment: first }));
    const duplicateResult = await duplicate.apply(
      envelope('session_snapshot', 2, { fragment: first }),
    );
    expect(duplicateResult.gapReason).toBe('non_contiguous_fragment');

    const trace = new DebugEventStore('dbg_abcdefghijklmnopqrstuv');
    const tracePart = {
      kind: 'trace' as const,
      encoding: 'base64url-json' as const,
      index: 0,
      count: 2,
      total_bytes: 2,
      sha256: '0'.repeat(64),
      data: 'eA',
    };
    await trace.apply(envelope('session_snapshot', 1, { metadata: metadata(), traces: [] }));
    await trace.apply(
      envelope('trace_upsert', 2, { fragment: tracePart }, { trace_id: 't', revision: 1 }),
    );
    const revisionResult = await trace.apply(
      envelope(
        'trace_upsert',
        3,
        { fragment: { ...tracePart, index: 1 } },
        { trace_id: 't', revision: 3 },
      ),
    );
    expect(revisionResult.gapReason).toBe('non_contiguous_fragment_revision');
  });

  it('fails closed when fragment hashing is unavailable', async () => {
    const store = new DebugEventStore('dbg_abcdefghijklmnopqrstuv');
    const document = JSON.stringify({ metadata: metadata({ last_event_id: 1 }), traces: [] });
    const parts = await fragmentParts(document, 1);
    vi.stubGlobal('crypto', {});
    try {
      const result = await store.apply(
        envelope('session_snapshot', 1, {
          fragment: {
            kind: 'snapshot',
            encoding: 'base64url-json',
            index: 0,
            count: 1,
            total_bytes: new TextEncoder().encode(document).byteLength,
            sha256: parts[0]?.sha256,
            data: parts[0]?.data,
          },
        }),
      );
      expect(result.gapReason).toBe('fragment_hash_unavailable');
      expect(result.recoveryFatal).toBe(true);
    } finally {
      vi.unstubAllGlobals();
    }
  });
});
