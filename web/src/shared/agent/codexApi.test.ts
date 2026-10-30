import { beforeEach, expect, it, vi } from 'vitest';
import { apiFetch } from '@shared/query/http';
import { beginCodex, getCodex, decodeCodexUsage, getCodexUsage } from './codexApi';
import { changeProvider, discoverProfileModels, newProfile, profileUpdate } from './driversApi';

vi.mock('@shared/query/http', async (original) => ({
  ...(await original<typeof import('@shared/query/http')>()),
  apiFetch: vi.fn(),
}));
const fetch = vi.mocked(apiFetch);

it('keeps usable quota buckets and unknown values without inventing zero usage', async () => {
  const payload = {
    official: {
      revision: '7',
      availability: 'available',
      fetched_at: 1000,
      data: {
        plan_type: 'business',
        buckets: [
          null,
          {
            id: 'codex',
            windows: [
              { name: 'primary', used_percent: null, window_seconds: 7200, resets_at: null },
              false,
            ],
            credits: { balance: '10.50', unlimited: null },
          },
          {
            id: 'other',
            windows: [
              { name: 'secondary', used_percent: 105, window_seconds: 600, resets_at: 1800000000 },
            ],
          },
        ],
        future: true,
      },
    },
    local: { availability: 'unavailable', data: null },
  };
  const parsed = decodeCodexUsage(payload);
  expect(parsed.official.data?.buckets).toHaveLength(2);
  expect(parsed.official.data?.buckets[0].windows[0].used_percent).toBeNull();
  expect(parsed.official.data?.buckets[1].windows[0].used_percent).toBe(105);
  expect(parsed.official.data?.buckets[0].credits?.unlimited).toBeNull();
  expect(parsed.local.data).toBeNull();
  fetch.mockResolvedValue(payload);
  await getCodexUsage(true);
  expect(fetch).toHaveBeenLastCalledWith('/admin/api/ai/codex/usage?refresh=1', expect.any(Object));
});
beforeEach(() => vi.clearAllMocks());
it('keeps device status in memory without accepting credential fields', async () => {
  fetch.mockResolvedValue({
    connected: false,
    revision: '3',
    account_id: '',
    account_label: '',
    expires_at: 0,
    access_token: 'private',
    login: {
      id: 'attempt',
      state: 'pending',
      user_code: 'TEST-CODE',
      expires_at: 1800000000,
      device_auth_id: 'secret-device',
    },
  });
  const value = await getCodex();
  expect(value.login?.user_code).toBe('TEST-CODE');
  expect(JSON.stringify(value)).not.toMatch(/private|secret-device/);
  await beginCodex('3');
  expect(fetch).toHaveBeenLastCalledWith(
    '/admin/api/ai/codex/login',
    expect.objectContaining({ json: { expected_revision: '3' }, method: 'POST' }),
  );
});
it('omits unsupported Codex sampling/output and reads the real model catalog projection', async () => {
  const profile = newProfile();
  profile.settings = changeProvider(profile.settings, 'codex');
  const update = profileUpdate(profile, {});
  expect(update.settings.max_output_tokens).toBe(0);
  expect(update.settings.base_url).toBe('');
  expect(update.settings).not.toHaveProperty('temperature');
  fetch.mockResolvedValue({ models: [{ id: 'codex-model', display_name: 'Model' }] });
  expect(await discoverProfileModels('1')).toEqual(['codex-model']);
});
