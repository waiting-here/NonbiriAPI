import { apiFetch } from '@shared/query/http';
import { decoded, idempotentOptions } from '@shared/operations/api';
import { normalizeDebugSession, type DebugMode, type DebugSessionMetadata } from './v2types';

export function getDebugSession(): Promise<DebugSessionMetadata> {
  return decoded('/api/debug/session', normalizeDebugSession);
}

export function startDebugSession(key: string): Promise<DebugSessionMetadata> {
  return decoded('/api/debug/session', normalizeDebugSession, idempotentOptions(key, { method: 'POST' }));
}

export function changeDebugMode(
  mode: DebugMode,
  expectedRevision: string,
  key: string,
): Promise<DebugSessionMetadata> {
  return decoded('/api/debug/session/mode', normalizeDebugSession, idempotentOptions(key, {
    method: 'PUT',
    json: { mode, expected_revision: expectedRevision, live_confirmation: mode === 'live' },
  }));
}

export async function stopDebugSession(
  expectedRevision: string,
  confirmInflight: boolean,
  key: string,
): Promise<void> {
  await apiFetch<void>('/api/debug/session/stop', idempotentOptions(key, {
    method: 'POST',
    json: { expected_revision: expectedRevision, confirm_inflight: confirmInflight },
  }));
}

export function replaceDebugSession(
  expectedRevision: string,
  confirmInflight: boolean,
  key: string,
): Promise<DebugSessionMetadata> {
  return decoded('/api/debug/session/replace', normalizeDebugSession, idempotentOptions(key, {
    method: 'POST',
    json: { expected_revision: expectedRevision, confirm_inflight: confirmInflight },
  }));
}
