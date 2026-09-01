import { ApiError } from '@shared/query/http';
import { normalizeDebugEvent, type DebugEvent } from './v2types';

export type DebugObserverStatus = 'connecting' | 'connected' | 'reconnecting' | 'disconnected';

export interface DebugStream {
  close: () => void;
}

export function openDebugStream(
  onEvent: (event: DebugEvent) => void,
  onStatus: (status: DebugObserverStatus) => void,
  onError: (error: unknown) => void,
): DebugStream {
  const source = new EventSource('/api/debug/events', { withCredentials: true });
  let closed = false;
  onStatus('connecting');
  source.onopen = () => onStatus('connected');
  source.onerror = () => {
    if (!closed) onStatus('reconnecting');
  };
  const receive = (message: MessageEvent<string>) => {
    try {
      const event = normalizeDebugEvent(JSON.parse(message.data) as unknown);
      if (message.lastEventId !== event.event_id) {
        throw new ApiError('invalid_response', 'The Debug event identity did not match its SSE id.', 200);
      }
      onEvent(event);
    } catch (error) {
      closed = true;
      source.close();
      onStatus('disconnected');
      onError(error);
    }
  };
  for (const kind of ['snapshot', 'trace_upsert', 'gap', 'session_end']) {
    source.addEventListener(kind, receive as EventListener);
  }
  return {
    close: () => {
      closed = true;
      source.close();
      onStatus('disconnected');
    },
  };
}
