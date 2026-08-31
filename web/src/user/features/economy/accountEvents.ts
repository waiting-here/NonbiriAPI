import { normalizeActivitiesSnapshot, isCanonicalSSEID } from './normalize';
import type { ActivitiesSnapshot } from './types';

type AccountEventName = 'snapshot' | 'delta' | 'gap';

export type ActivityAccountEventResult =
  | { kind: 'replace'; snapshot: ActivitiesSnapshot; revision: string }
  | { kind: 'resync'; reason: 'gap' | 'malformed' }
  | { kind: 'ignore' };

type StreamFetch = (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>;

interface ConnectOptions {
  onReplace: (snapshot: ActivitiesSnapshot, revision: string) => void;
  onResync: (reason: 'gap' | 'malformed' | 'disconnect' | 'reconnect') => void;
  onConnectionChange?: (state: 'connecting' | 'connected' | 'disconnected') => void;
  fetchStream?: StreamFetch;
  random?: () => number;
}

const FRAME_KEYS = [
  'version',
  'channel',
  'type',
  'revision',
  'identity_epoch',
  'occurred_at',
  'data',
] as const;
const GAP_KEYS = ['reason', 'last_event_id'] as const;
const REVISION = /^[1-9][0-9]*$/;
const GAP_REASONS = new Set(['process_restart', 'ring_expired', 'ring_evicted', 'slow_consumer']);
const MAX_SNAPSHOT_FRAME_BYTES = 64 * 1024;
const MAX_DELTA_FRAME_BYTES = 16 * 1024;
const MAX_GAP_FRAME_BYTES = 2 * 1024;
const MAX_REVISION = (1n << 128n) - 1n;
const MAX_SSE_RECORD_BYTES = MAX_SNAPSHOT_FRAME_BYTES + 1024;
const RECONNECT_CEILINGS_MS = [1_000, 2_000, 4_000, 8_000, 15_000] as const;
const SSE_CONTENT_TYPE = /^text\/event-stream(?:\s*;\s*charset=utf-8)?$/i;

interface SSERecord {
  eventName: AccountEventName;
  data: string;
  id: string;
}

class MalformedAccountEventStreamError extends Error {}

function exactRecord(value: unknown, keys: readonly string[]): Record<string, unknown> | null {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) return null;
  const item = value as Record<string, unknown>;
  const actual = Object.keys(item);
  if (actual.length !== keys.length || actual.some((key) => !keys.includes(key))) return null;
  return keys.every((key) => Object.prototype.hasOwnProperty.call(item, key)) ? item : null;
}

function malformed(): ActivityAccountEventResult {
  return { kind: 'resync', reason: 'malformed' };
}

export function parseActivityAccountEvent(
  eventName: AccountEventName,
  rawData: string,
  lastEventId: string,
): ActivityAccountEventResult {
  if (!isCanonicalSSEID(lastEventId)) return malformed();
  const maximumBytes =
    eventName === 'snapshot'
      ? MAX_SNAPSHOT_FRAME_BYTES
      : eventName === 'delta'
        ? MAX_DELTA_FRAME_BYTES
        : MAX_GAP_FRAME_BYTES;
  if (rawData.length > maximumBytes || new TextEncoder().encode(rawData).byteLength > maximumBytes)
    return malformed();
  let parsed: unknown;
  try {
    parsed = JSON.parse(rawData) as unknown;
  } catch {
    return malformed();
  }
  const frame = exactRecord(parsed, FRAME_KEYS);
  if (
    !frame ||
    frame.version !== 1 ||
    frame.type !== eventName ||
    (frame.channel !== 'activities' && frame.channel !== 'rps') ||
    !Number.isSafeInteger(frame.occurred_at) ||
    (frame.occurred_at as number) < 0 ||
    (frame.occurred_at as number) > 253_402_300_799
  ) {
    return malformed();
  }

  // The shared stream also carries RPS. This consumer never interprets or
  // caches that payload; the owning game feature validates its own union.
  if (frame.channel === 'rps') return { kind: 'ignore' };

  if (eventName === 'gap') {
    if (frame.revision !== null || frame.identity_epoch !== null) return malformed();
    const gap = exactRecord(frame.data, GAP_KEYS);
    if (!gap || typeof gap.reason !== 'string' || !GAP_REASONS.has(gap.reason)) return malformed();
    if (gap.last_event_id !== null && !isCanonicalSSEID(gap.last_event_id)) return malformed();
    return { kind: 'resync', reason: 'gap' };
  }

  if (
    frame.identity_epoch !== null ||
    typeof frame.revision !== 'string' ||
    !REVISION.test(frame.revision) ||
    frame.revision.length > 39 ||
    BigInt(frame.revision) > MAX_REVISION
  ) {
    return malformed();
  }
  try {
    return {
      kind: 'replace',
      snapshot: normalizeActivitiesSnapshot(frame.data),
      revision: frame.revision,
    };
  } catch {
    return malformed();
  }
}

function nextSSERecordBoundary(buffer: string): { start: number; end: number } | null {
  let earliest: { start: number; end: number } | null = null;
  for (const separator of ['\r\n\r\n', '\n\n', '\r\r']) {
    const start = buffer.indexOf(separator);
    if (start >= 0 && (earliest === null || start < earliest.start)) {
      earliest = { start, end: start + separator.length };
    }
  }
  return earliest;
}

function parseSSERecord(record: string): SSERecord | null {
  const values = new Map<string, string>();
  let hasField = false;
  for (const line of record.split(/\r\n|\n|\r/)) {
    if (line === '' || line.startsWith(':')) continue;
    hasField = true;
    const separator = line.indexOf(':');
    const field = separator < 0 ? line : line.slice(0, separator);
    let value = separator < 0 ? '' : line.slice(separator + 1);
    if (value.startsWith(' ')) value = value.slice(1);
    if (!['id', 'event', 'data'].includes(field) || values.has(field)) {
      throw new MalformedAccountEventStreamError('Invalid SSE control field.');
    }
    values.set(field, value);
  }
  if (!hasField) return null;
  if (values.size !== 3) {
    throw new MalformedAccountEventStreamError('Incomplete SSE record.');
  }
  const id = values.get('id');
  const eventName = values.get('event');
  const data = values.get('data');
  if (
    !isCanonicalSSEID(id) ||
    (eventName !== 'snapshot' && eventName !== 'delta' && eventName !== 'gap') ||
    data === undefined
  ) {
    throw new MalformedAccountEventStreamError('Invalid SSE record identity.');
  }
  return { eventName, data, id };
}

async function consumeAccountEventStream(
  response: Response,
  signal: AbortSignal,
  onRecord: (record: SSERecord) => void,
): Promise<void> {
  const contentType = response.headers.get('Content-Type')?.trim() ?? '';
  if (!response.ok || !SSE_CONTENT_TYPE.test(contentType) || !response.body) {
    throw new Error('The account event stream could not be opened.');
  }
  const reader = response.body.getReader();
  const decoder = new TextDecoder('utf-8', { fatal: true });
  let buffer = '';
  try {
    while (!signal.aborted) {
      const { value, done } = await reader.read();
      if (done) break;
      buffer += decoder.decode(value, { stream: true });
      let boundary = nextSSERecordBoundary(buffer);
      while (boundary) {
        const recordText = buffer.slice(0, boundary.start);
        buffer = buffer.slice(boundary.end);
        if (new TextEncoder().encode(recordText).byteLength > MAX_SSE_RECORD_BYTES) {
          throw new MalformedAccountEventStreamError('Oversized SSE record.');
        }
        const record = parseSSERecord(recordText);
        if (record) onRecord(record);
        boundary = nextSSERecordBoundary(buffer);
      }
      if (new TextEncoder().encode(buffer).byteLength > MAX_SSE_RECORD_BYTES) {
        throw new MalformedAccountEventStreamError('Oversized incomplete SSE record.');
      }
    }
    buffer += decoder.decode();
    if (buffer.trim() !== '') {
      throw new MalformedAccountEventStreamError('Truncated SSE record.');
    }
  } finally {
    reader.releaseLock();
  }
}

function reconnectDelay(attempt: number, random: () => number): number {
  const ceiling = RECONNECT_CEILINGS_MS[Math.min(attempt, RECONNECT_CEILINGS_MS.length - 1)];
  const sample = random();
  const bounded = Number.isFinite(sample) ? Math.min(1, Math.max(0, sample)) : 0.5;
  // Keep every jittered attempt below its frozen ceiling while avoiding a
  // near-zero reconnect herd after a shared network failure.
  return Math.floor(ceiling * (0.8 + bounded * 0.2));
}

export function connectActivityAccountEvents(options: ConnectOptions): () => void {
  const fetchStream = options.fetchStream ?? fetch;
  const random = options.random ?? Math.random;
  let closed = false;
  let disconnected = false;
  let reconnectAttempt = 0;
  let reconnectTimer: number | null = null;
  let activeController: AbortController | null = null;
  let lastEventId: string | null = null;
  let latestActivitiesRevision: bigint | null = null;
  options.onConnectionChange?.('connecting');

  const handle = (record: SSERecord) => {
    // Advancing a structurally valid cursor before payload reconciliation
    // prevents one malformed payload from replaying forever. The required GET
    // supplies authority for the skipped record.
    lastEventId = record.id;
    const result = parseActivityAccountEvent(record.eventName, record.data, record.id);
    if (result.kind === 'replace') {
      const revision = BigInt(result.revision);
      if (latestActivitiesRevision !== null && revision <= latestActivitiesRevision) return;
      latestActivitiesRevision = revision;
      options.onReplace(result.snapshot, result.revision);
    } else if (result.kind === 'resync') {
      // A gap is followed by a fresh authoritative snapshot. Its projection
      // revision may equal the last frame while time-derived state changed,
      // so the pre-gap monotonic gate cannot suppress that replacement.
      if (result.reason === 'gap') latestActivitiesRevision = null;
      options.onResync(result.reason);
    }
  };

  const markDisconnected = () => {
    latestActivitiesRevision = null;
    options.onConnectionChange?.('disconnected');
    if (!disconnected) {
      disconnected = true;
      options.onResync('disconnect');
    }
  };

  let openStream = () => undefined;
  const scheduleReconnect = () => {
    if (closed || reconnectTimer !== null || activeController !== null) return;
    const delay = reconnectDelay(reconnectAttempt, random);
    reconnectAttempt = Math.min(reconnectAttempt + 1, RECONNECT_CEILINGS_MS.length - 1);
    reconnectTimer = window.setTimeout(() => {
      reconnectTimer = null;
      openStream();
    }, delay);
  };

  openStream = () => {
    if (closed || activeController !== null) return;
    options.onConnectionChange?.('connecting');
    const controller = new AbortController();
    activeController = controller;
    const headers = new Headers({ Accept: 'text/event-stream' });
    if (lastEventId) headers.set('Last-Event-ID', lastEventId);
    void fetchStream('/api/events', {
      method: 'GET',
      headers,
      credentials: 'same-origin',
      cache: 'no-store',
      signal: controller.signal,
    })
      .then(async (response) => {
        if (closed || controller.signal.aborted) return;
        if (
          !response.ok ||
          !SSE_CONTENT_TYPE.test(response.headers.get('Content-Type')?.trim() ?? '')
        ) {
          throw new Error('The account event stream could not be opened.');
        }
        options.onConnectionChange?.('connected');
        if (disconnected) options.onResync('reconnect');
        disconnected = false;
        reconnectAttempt = 0;
        await consumeAccountEventStream(response, controller.signal, handle);
      })
      .catch((error: unknown) => {
        if (closed || controller.signal.aborted) return;
        if (error instanceof MalformedAccountEventStreamError) options.onResync('malformed');
      })
      .finally(() => {
        if (activeController === controller) activeController = null;
        if (closed || controller.signal.aborted) return;
        markDisconnected();
        scheduleReconnect();
      });
  };

  const reconnectNow = () => {
    if (closed || activeController !== null) return;
    if (reconnectTimer !== null) {
      window.clearTimeout(reconnectTimer);
      reconnectTimer = null;
    }
    reconnectAttempt = 0;
    openStream();
  };
  window.addEventListener('online', reconnectNow);
  window.addEventListener('focus', reconnectNow);
  openStream();

  return () => {
    closed = true;
    window.removeEventListener('online', reconnectNow);
    window.removeEventListener('focus', reconnectNow);
    if (reconnectTimer !== null) window.clearTimeout(reconnectTimer);
    activeController?.abort();
    activeController = null;
  };
}
