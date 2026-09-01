import { ApiError } from '@shared/query/http';
import type { DebugEndReason, DebugEvent, DebugGapReason, DebugSessionMetadata, DebugTrace } from './v2types';

export interface DebugViewState {
  session: DebugSessionMetadata;
  traces: DebugTrace[];
  gap: DebugGapReason | null;
  end: DebugEndReason | null;
  cancelled_inflight_count: number;
}

export function initialDebugState(session: DebugSessionMetadata = { active: false }): DebugViewState {
  return { session, traces: [], gap: null, end: null, cancelled_inflight_count: 0 };
}

function sameJSON(left: unknown, right: unknown): boolean {
  return JSON.stringify(left) === JSON.stringify(right);
}

export function applyDebugEvent(state: DebugViewState, event: DebugEvent): DebugViewState {
  if (!state.session.active || event.session_id !== state.session.id || event.generation !== state.session.generation) {
    throw new ApiError('invalid_response', 'The Debug stream changed session identity.', 200);
  }
  if (event.kind === 'gap') return { ...state, gap: event.data.reason };
  if (event.kind === 'session_end') {
    return { session: { active: false }, traces: [], gap: null, end: event.data.reason, cancelled_inflight_count: event.data.cancelled_inflight_count };
  }
  if (event.kind === 'snapshot') {
    if (event.data.session.id !== event.session_id || event.data.session.generation !== event.generation) {
      throw new ApiError('invalid_response', 'The Debug snapshot changed session identity.', 200);
    }
    return { session: event.data.session, traces: event.data.traces, gap: null, end: null, cancelled_inflight_count: 0 };
  }
  const existing = state.traces.find((trace) => trace.trace_id === event.data.trace_id);
  if (existing) {
    const currentRevision = BigInt(existing.revision);
    const nextRevision = BigInt(event.data.revision);
    if (nextRevision < currentRevision) return state;
    if (nextRevision === currentRevision) {
      if (!sameJSON(existing, event.data)) throw new ApiError('invalid_response', 'The Debug stream repeated a conflicting trace revision.', 200);
      return state;
    }
  }
  const traces = [...state.traces.filter((trace) => trace.trace_id !== event.data.trace_id), event.data]
    .sort((left, right) => left.created_at - right.created_at || left.trace_id.localeCompare(right.trace_id));
  return { ...state, traces: traces.slice(-32) };
}
