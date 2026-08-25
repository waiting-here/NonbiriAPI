import {
  decodeFragment,
  decodeEventEnvelope,
  decodeSessionMetadata,
  isRecordValue,
  isSessionEndReason,
  type DebugEventEnvelope,
  type DebugSessionMetadata,
  type DebugSessionSnapshot,
  type DebugTrace,
  type DebugTraceRecord,
  type FragmentPayload,
  type SessionEndReason,
} from './types';

const MAX_FRAGMENT_COUNT = 128;
const MAX_ASSEMBLED_BYTES = 4 * 1024 * 1024;
const MAX_TRACE_RECORDS = 32;
const TRACE_MODES = new Set(['dry', 'live']);
const TRACE_STATUSES = new Set([
  'received',
  'validated',
  'routing',
  'dispatching',
  'streaming',
  'completed',
  'dry_completed',
  'failed',
  'cancelled',
  'incomplete',
]);

interface FragmentGroup {
  kind: FragmentPayload['kind'];
  traceId: string;
  count: number;
  totalBytes: number;
  sha256: string;
  parts: Array<Uint8Array | undefined>;
  received: number;
  receivedBytes: number;
  lastSeq: number;
  lastRevision: number;
}

type FragmentResult =
  | { status: 'pending' }
  | { status: 'complete'; bytes: Uint8Array }
  | { status: 'invalid'; reason: string };

interface SnapshotDocument {
  metadata: unknown;
  traces: unknown;
  dropped?: unknown;
}

function cloneTrace(value: DebugTrace): DebugTrace {
  // The wire is JSON, so this clone intentionally strips prototypes and
  // accessors before any component can render untrusted data.
  try {
    const cloned: unknown = JSON.parse(JSON.stringify(value));
    return isRecordValue(cloned) ? cloned : {};
  } catch {
    return {};
  }
}

function finiteCounter(value: unknown): number {
  return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0 ? value : 0;
}

function droppedCounter(payload: Record<string, unknown>): { valid: boolean; value: number } {
  if (!Object.prototype.hasOwnProperty.call(payload, 'dropped')) return { valid: true, value: 0 };
  const value = payload.dropped;
  return {
    valid: typeof value === 'number' && Number.isSafeInteger(value) && value >= 0,
    value: typeof value === 'number' && Number.isSafeInteger(value) && value >= 0 ? value : 0,
  };
}

function base64UrlBytes(value: string): Uint8Array | null {
  if (!/^[A-Za-z0-9_-]*$/.test(value)) return null;
  if (value.length % 4 === 1) return null;
  const padded =
    value.replace(/-/g, '+').replace(/_/g, '/') + '='.repeat((4 - (value.length % 4)) % 4);
  try {
    const binary = atob(padded);
    const bytes = new Uint8Array(binary.length);
    for (let index = 0; index < binary.length; index += 1) bytes[index] = binary.charCodeAt(index);
    const canonical = btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
    if (canonical !== value) return null;
    return bytes;
  } catch {
    return null;
  }
}

async function sha256Hex(value: Uint8Array): Promise<string | null> {
  const subtle = globalThis.crypto?.subtle;
  if (!subtle) return null;
  try {
    const buffer = new ArrayBuffer(value.byteLength);
    new Uint8Array(buffer).set(value);
    const digest = await subtle.digest('SHA-256', buffer);
    return Array.from(new Uint8Array(digest), (byte) => byte.toString(16).padStart(2, '0')).join(
      '',
    );
  } catch {
    return null;
  }
}

function fragmentKey(event: DebugEventEnvelope, fragment: FragmentPayload): string {
  return `${event.type}|${event.trace_id}|${fragment.kind}`;
}

function validRequestID(value: unknown): value is string {
  return (
    typeof value === 'string' &&
    value.length > 0 &&
    new TextEncoder().encode(value).byteLength <= 128 &&
    !Array.from(value).some((character) => {
      const code = character.charCodeAt(0);
      return code <= 0x1f || code === 0x7f;
    })
  );
}

function traceIDFromSnapshot(value: DebugTrace): string | null {
  const requestID = value.request_id;
  return validRequestID(requestID) ? requestID : null;
}

function validTraceProjection(value: DebugTrace, expectedID?: string): boolean {
  return (
    validRequestID(value.request_id) &&
    (expectedID === undefined || value.request_id === expectedID) &&
    typeof value.mode === 'string' &&
    TRACE_MODES.has(value.mode) &&
    typeof value.terminal === 'string' &&
    TRACE_STATUSES.has(value.terminal)
  );
}

function safeGapReason(value: unknown): string {
  if (typeof value !== 'string' || value.length === 0) return 'resume_gap';
  return value.slice(0, 128);
}

function safeEndReason(value: unknown): SessionEndReason | null {
  return isSessionEndReason(value) ? value : null;
}

export class DebugEventStore {
  private state: DebugSessionSnapshot = {
    metadata: null,
    traces: [],
    lastEventId: 0,
    dropped: 0,
    gapReason: null,
    endReason: null,
    recoveryRequired: false,
    recoveryFatal: false,
  };

  private readonly fragments = new Map<string, FragmentGroup>();
  private expectedSessionID: string | undefined;

  constructor(expectedSessionID?: string) {
    this.expectedSessionID = expectedSessionID;
  }

  snapshot(): DebugSessionSnapshot {
    return {
      ...this.state,
      metadata: this.state.metadata
        ? { ...this.state.metadata, limits: { ...this.state.metadata.limits } }
        : null,
      traces: this.state.traces.map((trace) => ({ ...trace, payload: cloneTrace(trace.payload) })),
    };
  }

  clear(): DebugSessionSnapshot {
    this.fragments.clear();
    this.state = {
      metadata: null,
      traces: [],
      lastEventId: 0,
      dropped: 0,
      gapReason: null,
      endReason: null,
      recoveryRequired: false,
      recoveryFatal: false,
    };
    return this.snapshot();
  }

  clearSession(): DebugSessionSnapshot {
    this.expectedSessionID = undefined;
    return this.clear();
  }

  bindSession(sessionID: string): DebugSessionSnapshot {
    if (this.expectedSessionID && this.expectedSessionID !== sessionID) {
      this.state = {
        metadata: null,
        traces: [],
        lastEventId: 0,
        dropped: 0,
        gapReason: null,
        endReason: null,
        recoveryRequired: false,
        recoveryFatal: false,
      };
      this.fragments.clear();
    }
    this.expectedSessionID = sessionID;
    return this.snapshot();
  }

  clearTraces(): DebugSessionSnapshot {
    this.state = { ...this.state, traces: [], gapReason: null };
    return this.snapshot();
  }

  seedMetadata(metadata: DebugSessionMetadata): DebugSessionSnapshot {
    this.state = {
      ...this.state,
      metadata: { ...metadata, limits: { ...metadata.limits } },
      endReason: null,
      recoveryRequired: false,
      recoveryFatal: false,
    };
    return this.snapshot();
  }

  async apply(event: DebugEventEnvelope): Promise<DebugSessionSnapshot> {
    if (!this.expectedSessionID) return this.markInvalidRecovery('unbound_session');
    const decoded = decodeEventEnvelope(event);
    if (!decoded) return this.markInvalidRecovery('invalid_envelope');
    event = decoded;
    if (event.session_id !== this.expectedSessionID)
      return this.markInvalidRecovery('session_mismatch');
    if (this.state.recoveryRequired && event.type !== 'session_snapshot') {
      this.state = { ...this.state, lastEventId: event.seq };
      return this.markInvalidRecovery('unexpected_after_recovery');
    }
    if (!this.state.metadata && event.type === 'trace_upsert') {
      this.state = { ...this.state, lastEventId: event.seq };
      return this.markInvalidRecovery('missing_snapshot');
    }
    if (event.seq <= this.state.lastEventId) return this.snapshot();
    const activeFragment = this.fragments.values().next().value as FragmentGroup | undefined;
    if (activeFragment) {
      const fragment = decodeFragment(event.payload.fragment);
      const expectedType = activeFragment.kind === 'snapshot' ? 'session_snapshot' : 'trace_upsert';
      if (
        event.type !== expectedType ||
        event.trace_id !== activeFragment.traceId ||
        !fragment ||
        fragment.kind !== activeFragment.kind
      ) {
        this.state = { ...this.state, lastEventId: event.seq };
        return this.markInvalidRecovery('fragment_interleave');
      }
    }
    if (
      this.state.lastEventId > 0 &&
      event.type !== 'session_snapshot' &&
      event.type !== 'gap' &&
      event.seq !== this.state.lastEventId + 1
    ) {
      this.state = { ...this.state, lastEventId: event.seq };
      return this.markInvalidRecovery('sequence_gap');
    }
    this.state = { ...this.state, lastEventId: event.seq };

    switch (event.type) {
      case 'gap': {
        const dropped = droppedCounter(event.payload);
        if (!dropped.valid) return this.markInvalidRecovery('invalid_dropped');
        this.fragments.clear();
        this.state = {
          ...this.state,
          traces: [],
          gapReason: safeGapReason(event.payload.reason),
          dropped: Math.max(this.state.dropped, dropped.value),
          recoveryRequired: true,
          recoveryFatal: false,
        };
        return this.snapshot();
      }
      case 'session_end':
        this.fragments.clear();
        {
          const endReason = safeEndReason(event.payload.reason);
          if (!endReason) {
            this.markInvalidRecovery('invalid_session_end');
            return this.snapshot();
          }
          this.state = {
            ...this.state,
            metadata: this.state.metadata
              ? { ...this.state.metadata, mode: 'dry', connected: false, last_event_id: event.seq }
              : null,
            traces: [],
            endReason,
            gapReason: null,
            recoveryRequired: false,
            recoveryFatal: false,
          };
          return this.snapshot();
        }
      case 'session_snapshot':
        return this.applySnapshotEvent(event);
      case 'trace_upsert':
        return this.applyTraceEvent(event);
    }
  }

  private async applySnapshotEvent(event: DebugEventEnvelope): Promise<DebugSessionSnapshot> {
    const fragment = decodeFragment(event.payload.fragment);
    if (event.payload.fragment !== undefined && !fragment)
      return this.markInvalidRecovery('invalid_snapshot_fragment');
    if (fragment) {
      if (fragment.kind !== 'snapshot')
        return this.markInvalidRecovery('invalid_snapshot_fragment');
      const assembled = await this.acceptFragment(event, fragment);
      if (assembled.status === 'pending') {
        this.state = { ...this.state, recoveryRequired: true, recoveryFatal: false };
        return this.snapshot();
      }
      if (assembled.status === 'invalid') return this.markInvalidRecovery(assembled.reason);
      const decoded: unknown = this.parseJSON(assembled.bytes);
      if (!isRecordValue(decoded)) return this.markInvalidRecovery('invalid_snapshot');
      return this.replaceSnapshot(decoded as unknown as SnapshotDocument);
    }
    return this.replaceSnapshot(event.payload as unknown as SnapshotDocument);
  }

  private async applyTraceEvent(event: DebugEventEnvelope): Promise<DebugSessionSnapshot> {
    const dropped = droppedCounter(event.payload);
    if (!dropped.valid) return this.markInvalidRecovery('invalid_dropped');
    const fragment = decodeFragment(event.payload.fragment);
    if (event.payload.fragment !== undefined && !fragment)
      return this.markInvalidRecovery('invalid_trace_fragment');
    let trace: unknown = event.payload.trace;
    if (fragment) {
      if (fragment.kind !== 'trace') return this.markInvalidRecovery('invalid_trace_fragment');
      const assembled = await this.acceptFragment(event, fragment);
      if (assembled.status === 'pending') return this.snapshot();
      if (assembled.status === 'invalid') return this.markInvalidRecovery(assembled.reason);
      trace = this.parseJSON(assembled.bytes);
    }
    if (!isRecordValue(trace) || event.revision < 1 || !validTraceProjection(trace, event.trace_id))
      return this.markInvalidRecovery('invalid_trace');
    const existing = this.state.traces.find((item) => item.id === event.trace_id);
    // A non-monotonic revision is not a patch that can be guessed. Keep the
    // last safe projection and wait for a snapshot recovery.
    if (existing && event.revision <= existing.revision)
      return this.markInvalidRecovery('non_monotonic_revision');
    const nextRecord: DebugTraceRecord = {
      id: event.trace_id,
      revision: event.revision,
      payload: cloneTrace(trace),
    };
    const withoutExisting = this.state.traces.filter((item) => item.id !== event.trace_id);
    const nextTraces = [...withoutExisting, nextRecord].slice(-MAX_TRACE_RECORDS);
    this.state = {
      ...this.state,
      traces: nextTraces,
      gapReason: null,
      dropped: Math.max(this.state.dropped, dropped.value),
    };
    return this.snapshot();
  }

  private async acceptFragment(
    event: DebugEventEnvelope,
    fragment: FragmentPayload,
  ): Promise<FragmentResult> {
    if (
      fragment.count > MAX_FRAGMENT_COUNT ||
      fragment.total_bytes > MAX_ASSEMBLED_BYTES ||
      fragment.index >= fragment.count
    ) {
      return { status: 'invalid', reason: 'invalid_fragment_bounds' };
    }
    if (!/^[0-9a-f]{64}$/.test(fragment.sha256))
      return { status: 'invalid', reason: 'invalid_fragment_hash' };
    const bytes = base64UrlBytes(fragment.data);
    if (!bytes) return { status: 'invalid', reason: 'invalid_fragment_encoding' };
    const key = fragmentKey(event, fragment);
    let group = this.fragments.get(key);
    if (!group) {
      if (fragment.index !== 0)
        return { status: 'invalid', reason: 'non_contiguous_fragment_index' };
      group = {
        kind: fragment.kind,
        traceId: event.trace_id,
        count: fragment.count,
        totalBytes: fragment.total_bytes,
        sha256: fragment.sha256.toLowerCase(),
        parts: new Array<Uint8Array | undefined>(fragment.count),
        received: 0,
        receivedBytes: 0,
        lastSeq: event.seq - 1,
        lastRevision: event.revision - 1,
      };
      this.fragments.set(key, group);
    }
    if (
      group.count !== fragment.count ||
      group.totalBytes !== fragment.total_bytes ||
      group.sha256 !== fragment.sha256.toLowerCase() ||
      group.kind !== fragment.kind ||
      group.traceId !== event.trace_id
    ) {
      this.fragments.delete(key);
      return { status: 'invalid', reason: 'fragment_metadata_mismatch' };
    }
    if (fragment.index !== group.received || event.seq !== group.lastSeq + 1) {
      this.fragments.delete(key);
      return { status: 'invalid', reason: 'non_contiguous_fragment' };
    }
    if (fragment.kind === 'trace' && event.revision !== group.lastRevision + 1) {
      this.fragments.delete(key);
      return { status: 'invalid', reason: 'non_contiguous_fragment_revision' };
    }
    // Bound retained bytes as each part arrives. Waiting until final assembly
    // would let a malformed group cache up to count × event-size bytes while
    // claiming a much smaller total projection.
    if (bytes.byteLength > group.totalBytes - group.receivedBytes) {
      this.fragments.delete(key);
      return { status: 'invalid', reason: 'invalid_fragment_total' };
    }
    group.parts[fragment.index] = bytes;
    group.received += 1;
    group.receivedBytes += bytes.byteLength;
    group.lastSeq = event.seq;
    group.lastRevision = event.revision;
    if (group.received !== group.count) return { status: 'pending' };
    const assembled = new Uint8Array(group.totalBytes);
    let offset = 0;
    for (const part of group.parts) {
      if (!part || offset + part.byteLength > assembled.byteLength) {
        this.fragments.delete(key);
        return { status: 'invalid', reason: 'invalid_fragment_total' };
      }
      assembled.set(part, offset);
      offset += part.byteLength;
    }
    this.fragments.delete(key);
    if (offset !== group.totalBytes) return { status: 'invalid', reason: 'invalid_fragment_total' };
    const digest = await sha256Hex(assembled);
    if (!digest) return { status: 'invalid', reason: 'fragment_hash_unavailable' };
    if (digest !== group.sha256) return { status: 'invalid', reason: 'fragment_hash_mismatch' };
    return { status: 'complete', bytes: assembled };
  }

  private parseJSON(bytes: Uint8Array): unknown {
    try {
      return JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(bytes));
    } catch {
      return null;
    }
  }

  private replaceSnapshot(value: SnapshotDocument): DebugSessionSnapshot {
    this.fragments.clear();
    const metadata = decodeSessionMetadata(value.metadata);
    if (
      !metadata ||
      metadata.id !== this.expectedSessionID ||
      (this.state.metadata && metadata.generation !== this.state.metadata.generation) ||
      !Array.isArray(value.traces) ||
      metadata.last_event_id !== this.state.lastEventId ||
      ('dropped' in value &&
        (typeof value.dropped !== 'number' ||
          !Number.isSafeInteger(value.dropped) ||
          value.dropped < 0))
    ) {
      return this.markInvalidRecovery('invalid_snapshot');
    }
    const traces: DebugTraceRecord[] = [];
    const traceIDs = new Set<string>();
    if (value.traces.length > MAX_TRACE_RECORDS)
      return this.markInvalidRecovery('invalid_snapshot_trace_count');
    for (const item of value.traces) {
      if (!isRecordValue(item)) return this.markInvalidRecovery('invalid_snapshot_trace');
      const id = traceIDFromSnapshot(item);
      if (!id || !validTraceProjection(item) || traceIDs.has(id))
        return this.markInvalidRecovery('invalid_snapshot_trace');
      traceIDs.add(id);
      traces.push({ id, revision: 1, payload: cloneTrace(item) });
    }
    this.state = {
      metadata: { ...metadata, last_event_id: this.state.lastEventId },
      traces: traces.slice(-MAX_TRACE_RECORDS),
      lastEventId: this.state.lastEventId,
      dropped: Math.max(this.state.dropped, finiteCounter(value.dropped)),
      gapReason: null,
      endReason: null,
      recoveryRequired: false,
      recoveryFatal: false,
    };
    return this.snapshot();
  }

  markInvalidRecovery(
    reason: string,
    consumed?: { seq: number; sessionID: string },
  ): DebugSessionSnapshot {
    this.fragments.clear();
    this.state = {
      ...this.state,
      lastEventId:
        consumed &&
        consumed.sessionID === this.expectedSessionID &&
        Number.isSafeInteger(consumed.seq) &&
        consumed.seq > this.state.lastEventId
          ? consumed.seq
          : this.state.lastEventId,
      metadata: this.state.metadata
        ? { ...this.state.metadata, mode: 'dry', connected: false }
        : null,
      traces: [],
      gapReason: reason,
      recoveryRequired: true,
      recoveryFatal: true,
    };
    return this.snapshot();
  }
}
