import type { DebugMode, DebugSessionMetadata, SessionEndReason } from './types';

export type DebugConnectionState =
  | 'idle'
  | 'starting'
  | 'connecting'
  | 'connected'
  | 'reconnecting'
  | 'stopped'
  | 'replaced'
  | 'expired'
  | 'error';

export interface DebugSessionViewState {
  metadata: DebugSessionMetadata | null;
  connection: DebugConnectionState;
  error: string | null;
  endReason: SessionEndReason | null;
}

export type DebugSessionAction =
  | { type: 'start_requested' }
  | { type: 'session_received'; metadata: DebugSessionMetadata }
  | { type: 'metadata_event'; metadata: DebugSessionMetadata }
  | { type: 'connect_requested'; reconnect: boolean }
  | { type: 'connected' }
  | { type: 'safe_dry' }
  | { type: 'recovery_blocked'; message: string }
  | { type: 'session_end'; reason: SessionEndReason }
  | { type: 'stopped' }
  | { type: 'error'; message: string };

export const INITIAL_DEBUG_SESSION_STATE: DebugSessionViewState = {
  metadata: null,
  connection: 'idle',
  error: null,
  endReason: null,
};

function dryMetadata(metadata: DebugSessionMetadata | null): DebugSessionMetadata | null {
  return metadata ? { ...metadata, mode: 'dry', connected: false } : null;
}

export function debugSessionReducer(
  state: DebugSessionViewState,
  action: DebugSessionAction,
): DebugSessionViewState {
  switch (action.type) {
    case 'start_requested':
      return { ...state, connection: 'starting', error: null, endReason: null };
    case 'session_received':
      return {
        metadata: { ...action.metadata, mode: 'dry', connected: false },
        connection: 'connecting',
        error: null,
        endReason: null,
      };
    case 'metadata_event':
      return {
        ...state,
        metadata: action.metadata,
        error: null,
        endReason: null,
      };
    case 'connect_requested':
      return {
        ...state,
        metadata: action.reconnect ? dryMetadata(state.metadata) : state.metadata,
        connection: action.reconnect ? 'reconnecting' : 'connecting',
        error: null,
        endReason: null,
      };
    case 'connected':
      return { ...state, connection: 'connected', error: null };
    case 'safe_dry':
      return { ...state, metadata: dryMetadata(state.metadata), error: null };
    case 'recovery_blocked':
      return {
        ...state,
        metadata: dryMetadata(state.metadata),
        connection: 'error',
        error: action.message,
      };
    case 'session_end':
      return {
        ...state,
        metadata: action.reason === 'session_invalid' ? null : dryMetadata(state.metadata),
        connection:
          action.reason === 'replaced'
            ? 'replaced'
            : action.reason === 'stopped'
              ? 'stopped'
              : 'expired',
        error: null,
        endReason: action.reason,
      };
    case 'stopped':
      return { metadata: null, connection: 'stopped', error: null, endReason: 'stopped' };
    case 'error':
      return { ...state, connection: 'error', error: action.message };
  }
}

export function effectiveDebugMode(state: DebugSessionViewState): DebugMode {
  return state.connection === 'connected' && state.metadata?.mode === 'live' ? 'live' : 'dry';
}
