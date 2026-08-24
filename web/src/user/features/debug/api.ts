import { apiFetch } from '@shared/query/http';
import {
  decodeSessionMetadata,
  decodeSessionResponse,
  isDebugMode,
  type DebugMode,
  type DebugSessionMetadata,
  type DebugSessionResponse,
} from './types';

function invalidResponse(): Error {
  return new Error('The debug service returned an invalid response.');
}

function hasControlCharacters(value: string): boolean {
  return Array.from(value).some((character) => {
    const code = character.charCodeAt(0);
    return code <= 0x1f || code === 0x7f;
  });
}

function decodeConfirmationID(value: unknown): string | null {
  if (typeof value !== 'string' || !/^confirm_[A-Za-z0-9_-]{22}$/.test(value)) return null;
  if (hasControlCharacters(value)) return null;
  if (new TextEncoder().encode(value).byteLength !== 30) return null;
  return value;
}

export async function getDebugSession(): Promise<DebugSessionResponse> {
  const response: unknown = await apiFetch<unknown>('/api/debug/session', { method: 'GET' });
  const decoded = decodeSessionResponse(response);
  if (!decoded) throw invalidResponse();
  return decoded;
}

export async function startDebugSession(): Promise<DebugSessionMetadata> {
  const response: unknown = await apiFetch<unknown>('/api/debug/session', { method: 'POST' });
  const decoded = decodeSessionMetadata(response);
  if (!decoded || decoded.mode !== 'dry') throw invalidResponse();
  return decoded;
}

export async function stopDebugSession(): Promise<void> {
  await apiFetch<void>('/api/debug/session', { method: 'DELETE' });
}

export async function issueLiveChallenge(): Promise<string> {
  const response: unknown = await apiFetch<unknown>('/api/debug/session/live-challenge', {
    method: 'POST',
  });
  const candidate =
    response !== null && typeof response === 'object' && !Array.isArray(response)
      ? (response as Record<string, unknown>).confirmation_id
      : undefined;
  const confirmationID = decodeConfirmationID(candidate);
  if (!confirmationID) {
    throw invalidResponse();
  }
  return confirmationID;
}

export async function setDebugMode(
  mode: DebugMode,
  confirmationId?: string,
): Promise<DebugSessionMetadata> {
  if (!isDebugMode(mode)) throw new Error('The debug mode is invalid.');
  const json: Record<string, string> = { mode };
  if (mode === 'live') {
    if (!confirmationId) throw new Error('A live confirmation is required.');
    json.confirmation_id = confirmationId;
  }
  const response: unknown = await apiFetch<unknown>('/api/debug/session/mode', {
    method: 'PUT',
    json,
  });
  const decoded = decodeSessionMetadata(response);
  if (!decoded || decoded.mode !== mode) throw invalidResponse();
  return decoded;
}
