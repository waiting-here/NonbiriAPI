import { describe, expect, it, vi } from 'vitest';
import {
  getManagedBindings,
  normalizeAdminCharityModel,
  normalizeAdminDonation,
  normalizeStewardCharityModel,
  normalizeStewardDonation,
} from './charity';

const common = {
  id: '1',
  status: 'pending',
  revision: '1',
  description: 'Donor-safe description',
  review_result: null,
  expires_at: null,
  keys: [],
  reviewer: null,
  created_at: 1,
  updated_at: 1,
};

const model = {
  id: '1',
  provider: 'provider',
  model: 'model',
  full_name: '[公益]provider/model',
  enabled: true,
  pricing: { mode: 'per_request', user_price: '1', donor_reward: '0' },
  discount: { enabled: true, percent: 10, start_at: null, end_at: null },
  flatten_tool_calls: false,
  revision: '1',
  binding_revision: '0',
  binding_count: '0',
  rolling_success: { sample_count: '0', success_count: '0', percent: null },
  created_at: 1,
  updated_at: 1,
};

describe('role-safe charity wire', () => {
  it('accepts a deidentified administrator owner but requires a steward owner', () => {
    expect(normalizeAdminDonation({ ...common, owner: null }).owner).toBeNull();
    expect(normalizeStewardDonation({ ...common, owner: { user_id: '2', display_name: 'Owner' } }).owner).toEqual({ user_id: '2', display_name: 'Owner' });
    expect(() => normalizeStewardDonation({ ...common, owner: null })).toThrow(/owner/i);
  });

  it('keeps admin and steward projections closed and rejects secret-bearing additions', () => {
    expect(() => normalizeAdminDonation({ ...common, owner: null, secret: 'sk-never-project' })).toThrow(/invalid administrator donation/i);
    expect(() => normalizeStewardDonation({ ...common, owner: { user_id: '2', display_name: 'Owner', discord_id: 'private' } })).toThrow(/invalid steward donation owner/i);
    expect(() => normalizeAdminDonation({ ...common, owner: { user_id: '2', display_name: 'Owner' } })).toThrow(/invalid administrator donation owner/i);
  });
});

describe('charity model wire', () => {
  it('accepts the prefixed 133-rune model name and initial binding revision', () => {
    const provider = '😀'.repeat(64);
    const modelName = '🧪'.repeat(64);
    const fullName = `[公益]${provider}/${modelName}`;

    expect(Array.from(fullName)).toHaveLength(133);
    expect(new TextEncoder().encode(fullName)).toHaveLength(521);
    expect(
      normalizeAdminCharityModel({
        ...model,
        provider,
        model: modelName,
        full_name: fullName,
      }),
    ).toMatchObject({ full_name: fullName, binding_revision: '0' });
    expect(normalizeStewardCharityModel(model)).toMatchObject({
      full_name: '[公益]provider/model',
      binding_revision: '0',
    });
    expect(() =>
      normalizeAdminCharityModel({ ...model, full_name: 'provider/model' }),
    ).toThrow(/full name/i);
  });

  it('accepts independent and equal discount bounds but rejects a reversed window', () => {
    for (const [start_at, end_at] of [
      [10, null],
      [null, 10],
      [10, 10],
      [null, null],
    ] as const) {
      expect(
        normalizeAdminCharityModel({
          ...model,
          discount: { ...model.discount, start_at, end_at },
        }).discount,
      ).toMatchObject({ start_at, end_at });
    }

    expect(() =>
      normalizeAdminCharityModel({
        ...model,
        discount: { ...model.discount, start_at: 11, end_at: 10 },
      }),
    ).toThrow(/discount window/i);
  });

  it('accepts binding revision zero from the bindings API', async () => {
    const fetchMock = vi.fn<typeof fetch>(async () =>
      new Response(JSON.stringify({ bindings: [], binding_revision: '0' }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    );
    vi.stubGlobal('fetch', fetchMock);

    await expect(getManagedBindings('admin', '1')).resolves.toEqual({
      bindings: [],
      binding_revision: '0',
    });
  });
});
