import { beforeEach, describe, expect, it, vi } from 'vitest';
import { apiFetch } from '@shared/query/http';
import {
  changeProvider,
  effectiveBinding,
  getChatGPTTarget,
  getChatGPT,
  getDrivers,
  importChatGPT,
  newProfile,
  profileUpdate,
  saveBinding,
  type DriverConfiguration,
} from './driversApi';

vi.mock('@shared/query/http', async (original) => ({
  ...(await original<typeof import('@shared/query/http')>()),
  apiFetch: vi.fn(),
}));
const fetch = vi.mocked(apiFetch);
beforeEach(() => vi.clearAllMocks());

describe('Driver configuration contracts', () => {
  it('projects official quota availability and retained usage without inventing account totals', async () => {
    fetch.mockResolvedValue({
      connected: true,
      revision: '3',
      host_id: 'host',
      account_label: 'Synthetic',
      expires_at: 1000000000,
      needs_login: false,
      updated_at: 1000000000000,
      official_usage: {
        availability: 'unavailable',
        reason: 'not_exposed_by_public_api',
        usage_url: 'https://chatgpt.com/settings/usage',
        access_token: 'synthetic-secret',
      },
      local_usage: {
        scope: 'retained_completed_chatgpt_requests',
        window_start: 999913600000,
        window_end: 1000000000000,
        requests: '2',
        input_tokens: '9007199254740993',
        cached_input_tokens: '0',
        cache_write_input_tokens: '0',
        output_tokens: '3',
        reasoning_tokens: '0',
        unknown_count: '1',
      },
      last_limit_error: {
        code: 'subscription_sharing_usage_limit_exceeded',
        occurred_at: 999999999000,
      },
    });
    const result = await getChatGPT();
    expect(result.updated_at).toBe(1000000000000);
    expect(result.expires_at).toBe(1000000000);
    expect(result.official_usage?.availability).toBe('unavailable');
    expect(result.local_usage?.input_tokens).toBe('9007199254740993');
    expect(result.last_limit_error?.occurred_at).toBe(999999999000);
    expect(JSON.stringify(result)).not.toContain('synthetic-secret');
  });
  it('clears provider-specific values and omits ChatGPT sampling and output caps', () => {
    const initial = newProfile();
    const next = changeProvider(
      {
        ...initial.settings,
        base_url: 'https://synthetic.test/v1',
        has_key: true,
        model: 'old',
        temperature: 0.3,
        top_p: 0.4,
        extra_parameters: { previous: true },
      },
      'chatgpt',
    );
    expect(next).toMatchObject({
      provider: 'chatgpt',
      base_url: '',
      has_key: false,
      model: '',
      max_output_tokens: 0,
      extra_parameters: {},
    });
    const input = profileUpdate({ ...initial, settings: next }, {});
    expect(input.settings).not.toHaveProperty('temperature');
    expect(input.settings).not.toHaveProperty('top_p');
    expect(input.settings.max_output_tokens).toBe(0);
    expect(input.settings.output_reserve_tokens).toBe(4096);
    expect(changeProvider(next, 'responses')).toMatchObject({
      base_url: '',
      model: '',
      extra_parameters: {},
    });
  });
  it('resolves individual, purpose, default and original API in order', () => {
    const config: DriverConfiguration = {
      profiles: [],
      bindings: [
        { scope: 'default', actor_id: '0', driver_id: '1', revision: '1' },
        { scope: 'steward_chat', actor_id: '0', driver_id: '2', revision: '1' },
        { scope: 'steward_chat', actor_id: '7', driver_id: '3', revision: '1' },
      ],
    };
    expect(effectiveBinding(config, 'steward_chat', '7').driver_id).toBe('3');
    expect(effectiveBinding(config, 'steward_chat', '8').driver_id).toBe('2');
    expect(effectiveBinding(config, 'admin_chat').driver_id).toBe('1');
    expect(effectiveBinding({ profiles: [], bindings: [] }, 'admin_chat').driver_id).toBe('0');
  });
  it('keeps decimal identities and strips unexpected credentials from profiles', async () => {
    const initial = newProfile();
    fetch.mockResolvedValue({
      profiles: [
        {
          ...initial,
          id: '9007199254740993',
          api_key: 'synthetic-secret',
          settings: {
            ...initial.settings,
            driver_id: '9007199254740993',
            connection_revision: '3',
            api_key: 'synthetic-secret',
          },
        },
      ],
      bindings: [],
    });
    const result = await getDrivers();
    expect(result.profiles[0].id).toBe('9007199254740993');
    expect(result.profiles[0].settings.connection_revision).toBe('3');
    expect(JSON.stringify(result)).not.toContain('synthetic-secret');
  });
  it('restores inheritance with null and preserves encrypted account-switch metadata', async () => {
    fetch.mockResolvedValueOnce({
      scope: 'steward_chat',
      actor_id: '7',
      driver_id: '0',
      revision: '0',
    });
    await saveBinding({
      scope: 'steward_chat',
      actor_id: '7',
      driver_id: null,
      expected_revision: '2',
    });
    expect(fetch).toHaveBeenLastCalledWith(
      '/admin/api/ai/driver-bindings',
      expect.objectContaining({
        json: { scope: 'steward_chat', actor_id: '7', driver_id: null, expected_revision: '2' },
      }),
    );
    fetch.mockResolvedValueOnce({
      version: 1,
      host_id: 'synthetic-host',
      revision: '2',
      public_key: 'public',
      new_account: true,
    });
    await getChatGPTTarget(true);
    expect(fetch).toHaveBeenLastCalledWith(
      '/admin/api/ai/chatgpt/target',
      expect.objectContaining({ json: { new_account: true } }),
    );
    const envelope = {
      version: 1,
      host_id: 'synthetic-host',
      revision: '2',
      ephemeral_key: 'ephemeral',
      nonce: 'nonce',
      ciphertext: 'encrypted',
      new_account: true,
    };
    fetch.mockResolvedValueOnce({
      connected: true,
      revision: '3',
      host_id: 'synthetic-host',
      account_label: 'Synthetic',
      expires_at: 123,
      needs_login: false,
    });
    await importChatGPT(envelope);
    expect(fetch).toHaveBeenLastCalledWith(
      '/admin/api/ai/chatgpt/import',
      expect.objectContaining({ json: envelope }),
    );
  });
});
