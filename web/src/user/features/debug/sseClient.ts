import { ApiError } from '@shared/query/http';
import { decodeEventEnvelope, isDebugSessionID, type DebugEventEnvelope } from './types';

export interface DebugSSEOptions {
  signal?: AbortSignal;
  lastEventId?: number;
  onEvent: (event: DebugEventEnvelope) => void | Promise<void>;
  onInvalid?: (reason: string, consumed?: { seq: number; sessionID: string }) => void;
}

export class DebugStreamError extends Error {
  readonly status: number;

  constructor(message: string, status = 0) {
    super(message);
    this.name = 'DebugStreamError';
    this.status = status;
  }
}

const MAX_EVENT_BYTES = 512 * 1024;

function utf8Bytes(value: string): number {
  return new TextEncoder().encode(value).byteLength;
}

function streamError(response: Response): Error {
  if (response.status === 401)
    return new ApiError('unauthorized', 'Authentication is required.', response.status);
  if (response.status === 404)
    return new ApiError('not_found', 'The debug session was not found.', response.status);
  if (response.status === 503)
    return new ApiError(
      'service_unavailable',
      'The debug stream is temporarily unavailable.',
      response.status,
    );
  return new DebugStreamError('The debug stream could not be opened.', response.status);
}

/**
 * Consume the Debug Hub stream with fetch and an explicit cursor header. The
 * header makes bounded Last-Event-ID recovery possible while keeping the
 * session id out of URLs and query caches.
 */
export async function connectDebugEvents(options: DebugSSEOptions): Promise<void> {
  const headers = new Headers({
    Accept: 'text/event-stream',
    'Cache-Control': 'no-store',
  });
  if (
    options.lastEventId !== undefined &&
    Number.isSafeInteger(options.lastEventId) &&
    options.lastEventId > 0
  ) {
    headers.set('Last-Event-ID', String(options.lastEventId));
  }
  let response: Response;
  try {
    response = await fetch('/api/debug/events', {
      method: 'GET',
      cache: 'no-store',
      credentials: 'same-origin',
      headers,
      signal: options.signal,
    });
  } catch (error) {
    if (options.signal?.aborted) throw error;
    throw new DebugStreamError('The debug stream could not be opened.');
  }
  if (!response.ok) throw streamError(response);
  const contentType = response.headers.get('content-type')?.toLowerCase() ?? '';
  if (contentType.split(';', 1)[0]?.trim() !== 'text/event-stream') {
    throw new DebugStreamError(
      'The debug stream returned an unexpected content type.',
      response.status,
    );
  }
  if (!response.body)
    throw new DebugStreamError('The debug stream has no readable body.', response.status);

  const reader = response.body.getReader();
  const decoder = new TextDecoder('utf-8', { fatal: true });
  let buffer = '';
  let eventName = '';
  let eventId = '';
  let seenEventField = false;
  let seenIDField = false;
  let dataLines: string[] = [];
  let eventBytes = 0;
  let dataBytes = 0;

  const invalid = (reason: string, consumed?: { seq: number; sessionID: string }): never => {
    options.onInvalid?.(reason, consumed);
    throw new DebugStreamError('The debug stream sent an invalid event.', response.status);
  };

  const dispatch = async () => {
    if (dataLines.length === 0) {
      eventName = '';
      eventId = '';
      seenEventField = false;
      seenIDField = false;
      eventBytes = 0;
      dataBytes = 0;
      return;
    }
    const data = dataLines.join('\n');
    if (utf8Bytes(data) > MAX_EVENT_BYTES) invalid('event_too_large');
    dataLines = [];
    const wire: unknown = (() => {
      try {
        return JSON.parse(data);
      } catch {
        return null;
      }
    })();
    const envelope = decodeEventEnvelope(wire);
    const parsedID = /^\d+$/.test(eventId) ? Number(eventId) : Number.NaN;
    const strictID = Number.isSafeInteger(parsedID) && parsedID >= 1;
    const wireRecord =
      wire !== null && typeof wire === 'object' && !Array.isArray(wire)
        ? (wire as Record<string, unknown>)
        : null;
    const wireSeq = wireRecord?.seq;
    const wireSessionID = wireRecord?.session_id;
    const consumed =
      strictID &&
      typeof wireSeq === 'number' &&
      Number.isSafeInteger(wireSeq) &&
      wireSeq >= 1 &&
      wireSeq === parsedID &&
      isDebugSessionID(wireSessionID)
        ? { seq: parsedID, sessionID: wireSessionID }
        : undefined;
    // A malformed/unknown event terminates this stream. Continuing to consume
    // later higher-sequence deltas could silently skip the required snapshot
    // recovery and apply an unsafe revision.
    if (!envelope) {
      invalid('invalid_envelope', consumed);
      return;
    }
    if (!eventName || eventName !== envelope.type) invalid('event_type_mismatch', consumed);
    if (!strictID) invalid('invalid_event_id');
    if (parsedID !== envelope.seq) invalid('event_id_mismatch');
    await options.onEvent(envelope);
    eventName = '';
    eventId = '';
    seenEventField = false;
    seenIDField = false;
    eventBytes = 0;
    dataBytes = 0;
  };

  const consumeLine = async (line: string) => {
    eventBytes += utf8Bytes(line) + 1;
    if (eventBytes > MAX_EVENT_BYTES) invalid('event_too_large');
    if (line === '') {
      await dispatch();
      return;
    }
    if (line.startsWith(':')) return;
    const separator = line.indexOf(':');
    const field = separator === -1 ? line : line.slice(0, separator);
    const rawValue = separator === -1 ? '' : line.slice(separator + 1).replace(/^ /, '');
    switch (field) {
      case 'event':
        if (seenEventField) invalid('duplicate_event_field');
        seenEventField = true;
        eventName = rawValue;
        break;
      case 'id':
        if (seenIDField) invalid('duplicate_id_field');
        seenIDField = true;
        eventId = rawValue;
        break;
      case 'data':
        dataLines.push(rawValue);
        dataBytes += utf8Bytes(rawValue) + (dataLines.length > 1 ? 1 : 0);
        if (dataBytes > MAX_EVENT_BYTES) invalid('event_too_large');
        break;
      default:
        // SSE extension fields are not part of the Debug contract.
        break;
    }
  };

  while (true) {
    const chunk = await reader.read();
    if (chunk.done) break;
    try {
      buffer += decoder.decode(chunk.value, { stream: true });
    } catch {
      invalid('invalid_utf8');
    }
    if (utf8Bytes(buffer) > MAX_EVENT_BYTES && !buffer.includes('\n')) invalid('event_too_large');
    let newline = buffer.indexOf('\n');
    while (newline !== -1) {
      let line = buffer.slice(0, newline);
      buffer = buffer.slice(newline + 1);
      if (line.endsWith('\r')) line = line.slice(0, -1);
      await consumeLine(line);
      newline = buffer.indexOf('\n');
    }
  }
  try {
    buffer += decoder.decode();
  } catch {
    invalid('invalid_utf8');
  }
  if (utf8Bytes(buffer) > MAX_EVENT_BYTES) invalid('event_too_large');
  if (buffer.length > 0) {
    let line = buffer;
    if (line.endsWith('\r')) line = line.slice(0, -1);
    await consumeLine(line);
  }
  await dispatch();
}
