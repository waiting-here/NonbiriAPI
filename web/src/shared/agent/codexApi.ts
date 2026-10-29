import { apiFetch } from '@shared/query/http';
import { boolean, decimal, integer, record, string } from '@shared/operations/wire';

export interface CodexStatus {
  connected: boolean;
  revision: string;
  account_label: string;
  account_id: string;
  expires_at: number;
  login?: {
    id: string;
    state: string;
    user_code?: string;
    expires_at: number;
    error_code?: string;
  };
}

function decode(value: unknown): CodexStatus {
  const row = record(
    value,
    ['connected', 'revision', 'account_label', 'account_id', 'expires_at', 'login'],
    'Codex connection',
    ['connected', 'revision', 'account_label', 'account_id', 'expires_at'],
  );
  const login =
    row.login == null
      ? undefined
      : record(row.login, ['id', 'state', 'user_code', 'expires_at', 'error_code'], 'Codex login', [
          'id',
          'state',
          'expires_at',
        ]);
  return {
    connected: boolean(row.connected, 'connection'),
    revision: decimal(row.revision, 'revision'),
    account_label: string(row.account_label, 'account'),
    account_id: string(row.account_id, 'workspace'),
    expires_at: integer(row.expires_at, 'expiry'),
    ...(login
      ? {
          login: {
            id: string(login.id, 'login'),
            state: string(login.state, 'login state'),
            expires_at: integer(login.expires_at, 'expiry'),
            ...(login.user_code === undefined
              ? {}
              : { user_code: string(login.user_code, 'device code') }),
            ...(login.error_code === undefined
              ? {}
              : { error_code: string(login.error_code, 'login error') }),
          },
        }
      : {}),
  };
}
const root = '/admin/api/ai/codex';
export const getCodex = async (signal?: AbortSignal) => decode(await apiFetch(root, { signal }));
export const beginCodex = async (revision: string, signal?: AbortSignal) =>
  decode(
    await apiFetch(root + '/login', {
      method: 'POST',
      json: { expected_revision: revision },
      signal,
    }),
  );
export const cancelCodex = async (id: string, signal?: AbortSignal) =>
  decode(await apiFetch(root + '/cancel', { method: 'POST', json: { login_id: id }, signal }));
export const disconnectCodex = async (revision: string, signal?: AbortSignal) =>
  decode(
    await apiFetch(root + '/logout', {
      method: 'POST',
      json: { expected_revision: revision },
      signal,
    }),
  );
