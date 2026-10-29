import { apiFetch } from '@shared/query/http';
import { array, boolean, decimal, integer, record, string } from '@shared/operations/wire';
import type { DriverSettings } from './api';

export type DriverProvider = 'chat_completions' | 'responses' | 'chatgpt' | 'codex';
export type DriverScope = 'default' | 'auto_review' | 'admin_chat' | 'steward_chat';
export interface ProfileSettings extends DriverSettings {
  driver_id: string;
  provider: DriverProvider;
  output_reserve_tokens: number;
  connection_revision?: string;
}
export interface DriverProfile {
  id: string;
  name: string;
  settings: ProfileSettings;
}
export interface DriverBinding {
  scope: DriverScope;
  actor_id: string;
  driver_id: string;
  revision: string;
}
export interface DriverConfiguration {
  profiles: DriverProfile[];
  bindings: DriverBinding[];
}
export interface ProfileUpdate {
  expected_revision: string;
  name: string;
  settings: Omit<ProfileSettings, 'revision' | 'has_key' | 'driver_id' | 'connection_revision'>;
  api_key?: string;
}
export interface BindingUpdate {
  scope: DriverScope;
  actor_id: string;
  driver_id: string | null;
  expected_revision: string;
}
export interface ChatGPTStatus {
  connected: boolean;
  revision: string;
  host_id: string;
  account_label: string;
  expires_at: number;
  needs_login: boolean;
  updated_at?: number;
  official_usage?: { availability: string; reason: string; usage_url: string };
  local_usage?: {
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
  };
  last_limit_error?: { code: string; occurred_at: number } | null;
}
export interface ChatGPTEnvelope extends Record<string, unknown> {
  version: number;
  host_id: string;
  revision: string;
  ephemeral_key: string;
  nonce: string;
  ciphertext: string;
  new_account?: boolean;
}

const root = '/admin/api/ai';
function profile(value: unknown): DriverProfile {
  const row = record(value, ['id', 'name', 'settings'], 'driver');
  const settings = record(
    row.settings,
    [
      'driver_id',
      'provider',
      'revision',
      'base_url',
      'has_key',
      'model',
      'context_window',
      'output_reserve_tokens',
      'max_output_tokens',
      'temperature',
      'top_p',
      'extra_parameters',
      'concurrency',
      'timeout_seconds',
      'output_token_parameter',
      'connection_revision',
    ],
    'driver settings',
    [
      'provider',
      'revision',
      'base_url',
      'has_key',
      'model',
      'context_window',
      'output_reserve_tokens',
      'max_output_tokens',
      'extra_parameters',
      'concurrency',
      'timeout_seconds',
      'output_token_parameter',
    ],
  );
  return {
    id: decimal(row.id, 'driver id'),
    name: string(row.name, 'driver name'),
    settings: {
      ...settings,
      driver_id:
        settings.driver_id === undefined
          ? decimal(row.id, 'driver id')
          : decimal(settings.driver_id, 'driver id'),
      revision: decimal(settings.revision, 'driver revision'),
      ...(settings.connection_revision === undefined
        ? {}
        : {
            connection_revision: decimal(settings.connection_revision, 'connection revision'),
          }),
    } as ProfileSettings,
  };
}
function binding(value: unknown): DriverBinding {
  const row = record(value, ['scope', 'actor_id', 'driver_id', 'revision'], 'driver binding');
  return {
    scope: string(row.scope, 'binding scope') as DriverScope,
    actor_id: decimal(row.actor_id, 'binding actor'),
    driver_id: decimal(row.driver_id, 'binding driver'),
    revision: decimal(row.revision, 'binding revision'),
  };
}
function configuration(value: unknown): DriverConfiguration {
  const row = record(value, ['profiles', 'bindings'], 'driver configuration');
  return {
    profiles: array(row.profiles, 'drivers').map(profile),
    bindings: array(row.bindings, 'bindings').map(binding),
  };
}
function status(value: unknown): ChatGPTStatus {
  const row = record(
    value,
    [
      'connected',
      'revision',
      'host_id',
      'account_label',
      'expires_at',
      'needs_login',
      'updated_at',
      'official_usage',
      'local_usage',
      'last_limit_error',
    ],
    'ChatGPT connection',
    ['connected', 'revision', 'host_id', 'account_label', 'expires_at', 'needs_login'],
  );
  const official =
    row.official_usage === undefined
      ? undefined
      : record(row.official_usage, ['availability', 'reason', 'usage_url'], 'official usage');
  const local =
    row.local_usage === undefined
      ? undefined
      : record(
          row.local_usage,
          [
            'scope',
            'window_start',
            'window_end',
            'requests',
            'input_tokens',
            'cached_input_tokens',
            'cache_write_input_tokens',
            'output_tokens',
            'reasoning_tokens',
            'unknown_count',
          ],
          'local usage',
        );
  const limit =
    row.last_limit_error === undefined || row.last_limit_error === null
      ? null
      : record(row.last_limit_error, ['code', 'occurred_at'], 'limit error');
  return {
    connected: boolean(row.connected, 'connection status'),
    revision: decimal(row.revision, 'connection revision'),
    host_id: string(row.host_id, 'host'),
    account_label: string(row.account_label, 'account label'),
    expires_at: integer(row.expires_at, 'expiry'),
    needs_login: boolean(row.needs_login, 'login status'),
    ...(row.updated_at === undefined
      ? {}
      : { updated_at: integer(row.updated_at, 'connection update') }),
    ...(official
      ? {
          official_usage: {
            availability: string(official.availability, 'usage availability'),
            reason: string(official.reason, 'usage reason'),
            usage_url: string(official.usage_url, 'usage URL'),
          },
        }
      : {}),
    ...(local
      ? {
          local_usage: {
            scope: string(local.scope, 'usage scope'),
            window_start: integer(local.window_start, 'usage start'),
            window_end: integer(local.window_end, 'usage end'),
            requests: decimal(local.requests, 'requests'),
            input_tokens: decimal(local.input_tokens, 'input tokens'),
            cached_input_tokens: decimal(local.cached_input_tokens, 'cached input tokens'),
            cache_write_input_tokens: decimal(
              local.cache_write_input_tokens,
              'cache write input tokens',
            ),
            output_tokens: decimal(local.output_tokens, 'output tokens'),
            reasoning_tokens: decimal(local.reasoning_tokens, 'reasoning tokens'),
            unknown_count: decimal(local.unknown_count, 'unknown count'),
          },
        }
      : {}),
    last_limit_error: limit
      ? {
          code: string(limit.code, 'limit error code'),
          occurred_at: integer(limit.occurred_at, 'limit error time'),
        }
      : null,
  };
}
export const getDrivers = async (signal?: AbortSignal) =>
  configuration(await apiFetch(root + '/drivers', { signal }));
export const saveProfile = async (id: string | null, input: ProfileUpdate, signal?: AbortSignal) =>
  profile(
    await apiFetch(root + '/drivers' + (id ? '/' + encodeURIComponent(id) : ''), {
      method: id ? 'PUT' : 'POST',
      json: input,
      signal,
    }),
  );
export const deleteProfile = async (id: string, revision: string, signal?: AbortSignal) => {
  await apiFetch(root + '/drivers/' + encodeURIComponent(id), {
    method: 'DELETE',
    json: { expected_revision: revision },
    signal,
  });
};
export const saveBinding = async (input: BindingUpdate, signal?: AbortSignal) =>
  binding(await apiFetch(root + '/driver-bindings', { method: 'PUT', json: input, signal }));
export const discoverProfileModels = async (id: string, signal?: AbortSignal) => {
  const row = record(
    await apiFetch(root + '/drivers/' + encodeURIComponent(id) + '/models', {
      method: 'POST',
      json: {},
      signal,
    }),
    ['models'],
    'models',
  );
  return array(row.models, 'models').map((value) =>
    typeof value === 'string' ? value : string(record(value, ['id'], 'model').id, 'model'),
  );
};
export const getChatGPT = async (signal?: AbortSignal) =>
  status(await apiFetch(root + '/chatgpt', { signal }));
export const getChatGPTTarget = async (newAccount: boolean, signal?: AbortSignal) => {
  const row = record(
    await apiFetch(root + '/chatgpt/target', {
      method: 'POST',
      json: { new_account: newAccount },
      signal,
    }),
    ['version', 'host_id', 'revision', 'public_key', 'client_id', 'subject', 'new_account'],
    'connection target',
    ['version', 'host_id', 'revision', 'public_key'],
  );
  decimal(row.revision, 'target revision');
  return row;
};
export const importChatGPT = async (envelope: ChatGPTEnvelope, signal?: AbortSignal) =>
  status(await apiFetch(root + '/chatgpt/import', { method: 'POST', json: envelope, signal }));
export const logoutChatGPT = async (revision: string, signal?: AbortSignal) => {
  const row = record(
    await apiFetch(root + '/chatgpt/logout', {
      method: 'POST',
      json: { expected_revision: revision },
      signal,
    }),
    ['remote_revocation_confirmed'],
    'logout',
  );
  return {
    remote_revocation_confirmed: boolean(row.remote_revocation_confirmed, 'remote revocation'),
  };
};

export function newProfile(): DriverProfile {
  return {
    id: '',
    name: 'Chat Completions API',
    settings: {
      driver_id: '',
      revision: '0',
      provider: 'chat_completions',
      base_url: '',
      has_key: false,
      model: '',
      context_window: 32768,
      output_reserve_tokens: 4096,
      max_output_tokens: 4096,
      extra_parameters: {},
      concurrency: 1,
      timeout_seconds: 600,
      output_token_parameter: 'max_tokens',
    },
  };
}
export function changeProvider(
  settings: ProfileSettings,
  provider: DriverProvider,
): ProfileSettings {
  return {
    ...settings,
    provider,
    base_url: '',
    has_key: false,
    model: '',
    temperature: undefined,
    top_p: undefined,
    extra_parameters: {},
    max_output_tokens: provider === 'chatgpt' || provider === 'codex' ? 0 : 4096,
    output_token_parameter: 'max_tokens',
  };
}
export function profileUpdate(
  profile: DriverProfile,
  extra: Record<string, unknown>,
): ProfileUpdate {
  const s = profile.settings;
  return {
    expected_revision: s.revision,
    name: profile.name.trim(),
    settings: {
      provider: s.provider,
      base_url: s.provider === 'chatgpt' || s.provider === 'codex' ? '' : s.base_url,
      model: s.model,
      context_window: s.context_window,
      output_reserve_tokens: s.output_reserve_tokens,
      max_output_tokens:
        s.provider === 'chatgpt' || s.provider === 'codex' ? 0 : s.max_output_tokens,
      output_token_parameter: s.output_token_parameter,
      concurrency: s.concurrency,
      timeout_seconds: s.timeout_seconds,
      extra_parameters: extra,
      ...(s.provider === 'chatgpt' || s.provider === 'codex'
        ? {}
        : { temperature: s.temperature, top_p: s.top_p }),
    },
  };
}
export function effectiveBinding(config: DriverConfiguration, scope: DriverScope, actor = '0') {
  const candidates = [
    ...(scope === 'steward_chat' && actor !== '0' ? [{ scope, actor_id: actor }] : []),
    ...(scope !== 'default' ? [{ scope, actor_id: '0' }] : []),
    { scope: 'default', actor_id: '0' },
  ];
  for (const candidate of candidates) {
    const found = config.bindings.find(
      (b) => b.scope === candidate.scope && b.actor_id === candidate.actor_id,
    );
    if (found) return found;
  }
  return { scope: 'legacy', actor_id: '0', driver_id: '0', revision: '0' };
}
