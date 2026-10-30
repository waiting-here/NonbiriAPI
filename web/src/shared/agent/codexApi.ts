import { decodeOperationFailure, type OperationFailure } from './DriverFailure';
import { apiFetch } from '@shared/query/http';
import { boolean, decimal, integer, record, string } from '@shared/operations/wire';

export interface CodexStatus {
  connected: boolean;
  revision: string;
  account_label: string;
  account_id: string;
  expires_at: number;
  updated_at?: number;
  login?: {
    id: string;
    state: string;
    user_code?: string;
    expires_at: number;
    error_code?: string;
    error?: OperationFailure | null;
  };
}

function decode(value: unknown): CodexStatus {
  const row = record(
    value,
    ['connected', 'revision', 'account_label', 'account_id', 'expires_at', 'updated_at', 'login'],
    'Codex connection',
    ['connected', 'revision', 'account_label', 'account_id', 'expires_at'],
  );
  const login =
    row.login == null
      ? undefined
      : record(
          row.login,
          ['id', 'state', 'user_code', 'expires_at', 'error_code', 'error'],
          'Codex login',
          ['id', 'state', 'expires_at'],
        );
  return {
    connected: boolean(row.connected, 'connection'),
    revision: decimal(row.revision, 'revision'),
    account_label: string(row.account_label, 'account'),
    account_id: string(row.account_id, 'workspace'),
    expires_at: integer(row.expires_at, 'expiry'),
    updated_at: typeof row.updated_at === 'number' ? row.updated_at : undefined,
    ...(login
      ? {
          login: {
            id: string(login.id, 'login'),
            state: string(login.state, 'login state'),
            error: decodeOperationFailure(login.error),
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

export interface QuotaWindow {
  name: string;
  used_percent: number | null;
  window_seconds: number | null;
  resets_at: number | null;
}
export interface QuotaBucket {
  id: string;
  name?: string;
  plan_type?: string;
  windows: QuotaWindow[];
  credits?: {
    has_credits: boolean | null;
    unlimited: boolean | null;
    balance?: string;
    unit?: string;
  };
  reached_type?: string;
  allowed?: boolean;
  limit_reached?: boolean;
}
export interface CodexUsage {
  official: {
    source: string;
    revision: string;
    availability: string;
    fetched_at: number;
    attempted_at: number;
    stale: boolean;
    data: {
      plan_type?: string;
      workspace_name?: string;
      buckets: QuotaBucket[];
      partial: boolean;
      reset_credits_available: number | null;
    } | null;
    error: OperationFailure | null;
  };
  local: {
    source: string;
    availability: string;
    fetched_at: number;
    error: OperationFailure | null;
    data: {
      scope: string;
      window_start: number;
      window_end: number;
      requests: string;
      input_tokens: string;
      cached_input_tokens: string;
      cache_write_input_tokens: string;
      output_tokens: string;
      reasoning_tokens: string;
      unknown_count: string;
    } | null;
  };
}
function objectOrNull(value: unknown): Record<string, unknown> | null {
  return value && typeof value === 'object' && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : null;
}
const finite = (value: unknown) =>
  typeof value === 'number' && Number.isFinite(value) ? value : null;
const text = (value: unknown) => (typeof value === 'string' ? value : undefined);
const flag = (value: unknown) => (typeof value === 'boolean' ? value : null);
export function decodeCodexUsage(value: unknown): CodexUsage {
  const row = record(value, ['official', 'local'], 'Codex usage');
  const official = record(
    row.official,
    ['revision', 'availability', 'fetched_at', 'attempted_at', 'stale', 'data', 'error', 'source'],
    'Codex quota',
    ['revision', 'availability'],
  );
  const local = record(
    row.local,
    ['availability', 'fetched_at', 'data', 'error', 'source'],
    'Codex local usage',
    ['availability'],
  );
  const data = objectOrNull(official.data);
  const localData = objectOrNull(local.data);
  const buckets: QuotaBucket[] = [];
  if (Array.isArray(data?.buckets))
    for (const value of data.buckets) {
      const b = objectOrNull(value);
      if (!b || typeof b.id !== 'string') continue;
      const windows: QuotaWindow[] = [];
      if (Array.isArray(b.windows))
        for (const value of b.windows) {
          const w = objectOrNull(value);
          if (!w || typeof w.name !== 'string') continue;
          windows.push({
            name: w.name,
            used_percent: finite(w.used_percent),
            window_seconds: finite(w.window_seconds),
            resets_at: finite(w.resets_at),
          });
        }
      const c = objectOrNull(b.credits);
      buckets.push({
        id: b.id,
        name: text(b.name),
        plan_type: text(b.plan_type),
        windows,
        reached_type: text(b.reached_type),
        allowed: flag(b.allowed) ?? undefined,
        limit_reached: flag(b.limit_reached) ?? undefined,
        ...(c
          ? {
              credits: {
                has_credits: flag(c.has_credits),
                unlimited: flag(c.unlimited),
                balance: text(c.balance),
                unit: text(c.unit),
              },
            }
          : {}),
      });
    }
  return {
    official: {
      source: text(official.source) ?? 'openai_codex',
      revision: decimal(official.revision, 'connection revision'),
      availability: string(official.availability, 'quota availability'),
      fetched_at: finite(official.fetched_at) ?? 0,
      attempted_at: finite(official.attempted_at) ?? 0,
      stale: official.stale === true,
      error: decodeOperationFailure(official.error),
      data: data
        ? {
            plan_type: text(data.plan_type),
            workspace_name: text(data.workspace_name),
            buckets,
            partial: data.partial === true,
            reset_credits_available: finite(data.reset_credits_available),
          }
        : null,
    },
    local: {
      source: text(local.source) ?? 'retained_instance_requests',
      availability: string(local.availability, 'usage availability'),
      fetched_at: finite(local.fetched_at) ?? 0,
      error: decodeOperationFailure(local.error),
      data: localData
        ? {
            scope: text(localData.scope) ?? '',
            window_start: finite(localData.window_start) ?? 0,
            window_end: finite(localData.window_end) ?? 0,
            requests: decimal(localData.requests, 'requests'),
            input_tokens: decimal(localData.input_tokens, 'input tokens'),
            cached_input_tokens: decimal(localData.cached_input_tokens, 'cache tokens'),
            cache_write_input_tokens: decimal(
              localData.cache_write_input_tokens,
              'cache write tokens',
            ),
            output_tokens: decimal(localData.output_tokens, 'output tokens'),
            reasoning_tokens: decimal(localData.reasoning_tokens, 'reasoning tokens'),
            unknown_count: decimal(localData.unknown_count, 'unknown requests'),
          }
        : null,
    },
  };
}
export const getCodexUsage = async (refresh = false, signal?: AbortSignal) =>
  decodeCodexUsage(await apiFetch(root + '/usage' + (refresh ? '?refresh=1' : ''), { signal }));
