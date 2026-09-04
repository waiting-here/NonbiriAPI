import { decimalValue, exactRecord, opaqueID, unixTime } from '../common/strict';
import { normalizeRPSHome, rpsStateEpoch, rpsStateRevision } from './normalize';
import type { RPSHomeState } from './types';

type EventName = 'snapshot' | 'delta' | 'gap';
export type RPSStreamResult =
  | { readonly kind: 'replace'; readonly home: RPSHomeState }
  | { readonly kind: 'resync'; readonly reason: 'gap' | 'malformed' }
  | { readonly kind: 'ignore' };
const GAP_REASONS = ['process_restart', 'ring_expired', 'ring_evicted', 'slow_consumer'] as const;

export function parseRPSStreamEvent(
  eventName: EventName,
  raw: string,
  lastEventID: string,
): RPSStreamResult {
  try {
    opaqueID(lastEventID, 'sse_', 'SSE event id');
    if (
      new TextEncoder().encode(raw).byteLength >
      (eventName === 'snapshot' ? 65_536 : eventName === 'delta' ? 16_384 : 2_048)
    )
      return { kind: 'resync', reason: 'malformed' };
    const frame = exactRecord(
      JSON.parse(raw) as unknown,
      ['version', 'channel', 'type', 'revision', 'identity_epoch', 'occurred_at', 'data'],
      [],
      'account event frame',
    );
    if (
      frame.version !== 1 ||
      frame.type !== eventName ||
      (frame.channel !== 'activities' && frame.channel !== 'rps')
    )
      return { kind: 'resync', reason: 'malformed' };
    unixTime(frame.occurred_at, 'account event time');
    if (frame.channel === 'activities') return { kind: 'ignore' };
    if (eventName === 'gap') {
      if (frame.revision !== null || frame.identity_epoch !== null)
        return { kind: 'resync', reason: 'malformed' };
      const gap = exactRecord(frame.data, ['reason', 'last_event_id'], [], 'RPS stream gap');
      if (
        typeof gap.reason !== 'string' ||
        !GAP_REASONS.includes(gap.reason as (typeof GAP_REASONS)[number])
      )
        return { kind: 'resync', reason: 'malformed' };
      if (gap.last_event_id !== null) opaqueID(gap.last_event_id, 'sse_', 'RPS gap event id');
      return { kind: 'resync', reason: 'gap' };
    }
    const home = normalizeRPSHome(frame.data);
    const revision =
      frame.revision === null
        ? null
        : decimalValue(frame.revision, { bits: 128, positive: true }, 'RPS stream revision');
    const epoch =
      frame.identity_epoch === null
        ? null
        : decimalValue(frame.identity_epoch, { bits: 128, positive: true }, 'RPS stream epoch');
    if (revision !== rpsStateRevision(home) || epoch !== rpsStateEpoch(home))
      return { kind: 'resync', reason: 'malformed' };
    return { kind: 'replace', home };
  } catch {
    return { kind: 'resync', reason: 'malformed' };
  }
}

export function connectRPSStream(options: {
  readonly onReplace: (home: RPSHomeState) => void;
  readonly onResync: (reason: 'gap' | 'malformed' | 'disconnect') => void;
  readonly onConnection: (state: 'connecting' | 'connected' | 'disconnected') => void;
  readonly EventSourceImpl?: typeof EventSource;
}): () => void {
  const Source = options.EventSourceImpl ?? EventSource;
  options.onConnection('connecting');
  const source = new Source('/api/events', { withCredentials: true });
  source.onopen = () => options.onConnection('connected');
  source.onerror = () => {
    options.onConnection('disconnected');
    options.onResync('disconnect');
  };
  const listen = (name: EventName) =>
    source.addEventListener(name, (raw) => {
      const event = raw as MessageEvent<string>;
      const result = parseRPSStreamEvent(name, event.data, event.lastEventId);
      if (result.kind === 'replace') options.onReplace(result.home);
      else if (result.kind === 'resync') options.onResync(result.reason);
    });
  listen('snapshot');
  listen('delta');
  listen('gap');
  return () => source.close();
}
