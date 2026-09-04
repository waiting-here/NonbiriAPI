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

const managedKey = {
  id: '11',
  endpoint_key_id: '21',
  display_head: 'A'.repeat(16),
  display_tail: 'Z'.repeat(16),
  safe_source: {
    kind: 'mainstream',
    channel_id: `mch_${'A'.repeat(22)}`,
    name: 'Frozen channel',
    connector_type: 'openai-compatible',
    base_url: 'https://example.test/v1',
  },
  physical_enabled: true,
  charity_state: 'available',
  limits: { price: null, calls: null, tokens: null },
  usage: {
    price_used: '0',
    price_inflight: '0',
    calls_used: '0',
    calls_inflight: '0',
    tokens_used: '0',
    tokens_inflight: '0',
  },
  token_reserve: 0,
  authorized_expires_at: null,
  expires_at: 100,
  streak: { generation: '1', count: '0', failure_disabled: false },
  ended_reason: null,
  safe_note: '',
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
    expect(() => normalizeAdminDonation({ ...common, owner: null, expires_at: null })).toThrow(
      /invalid administrator donation/i,
    );
  });

  it('keeps provenance role-specific and enforces the donor expiry ceiling', () => {
    const adminSource = {
      ...managedKey.safe_source,
      channel_revision: '3',
      category: 'subscription',
    };
    const admin = normalizeAdminDonation({
      ...common,
      keys: [{ ...managedKey, safe_source: adminSource }],
      owner: null,
    });
    expect(admin.keys[0]).toMatchObject({
      display_head: 'A'.repeat(16),
      authorized_expires_at: null,
      expires_at: 100,
      safe_source: { kind: 'mainstream', channel_revision: '3', category: 'subscription' },
    });

    const stewardOwner = { user_id: '2', display_name: 'Owner' };
    expect(
      normalizeStewardDonation({ ...common, keys: [managedKey], owner: stewardOwner }).keys[0]
        .safe_source,
    ).not.toHaveProperty('category');
    expect(() =>
      normalizeStewardDonation({
        ...common,
        keys: [{ ...managedKey, safe_source: adminSource }],
        owner: stewardOwner,
      }),
    ).toThrow(/source/i);
    expect(() =>
      normalizeAdminDonation({
        ...common,
        keys: [
          {
            ...managedKey,
            safe_source: adminSource,
            authorized_expires_at: 99,
            expires_at: 100,
          },
        ],
        owner: null,
      }),
    ).toThrow(/expiry authorization/i);
    expect(() =>
      normalizeAdminDonation({
        ...common,
        keys: [{ ...managedKey, safe_source: adminSource, ended_reason: 'unknown' }],
        owner: null,
      }),
    ).toThrow(/ended reason/i);
  });

  it('accepts an automatic mainstream approval with an empty system review reason', () => {
    const adminSource = {
      ...managedKey.safe_source,
      channel_revision: '3',
      category: 'subscription',
    };
    const result = normalizeAdminDonation({
      ...common,
      status: 'approved',
      review_result: { decision: 'approve', reason: '', reviewed_at: 1 },
      reviewer: null,
      keys: [{ ...managedKey, safe_source: adminSource }],
      owner: null,
    });
    expect(result.review_result).toEqual({ decision: 'approve', reason: '', reviewed_at: 1 });
  });

  it('accepts a clock-expired key before lifecycle cleanup records its terminal reason', () => {
    const result = normalizeStewardDonation({
      ...common,
      status: 'approved',
      review_result: { decision: 'approve', reason: 'accepted', reviewed_at: 1 },
      reviewer: { user_id: '9', role: 'admin' },
      keys: [{ ...managedKey, charity_state: 'expired', ended_reason: null }],
      owner: { user_id: '2', display_name: 'Owner' },
    });
    expect(result.keys[0]).toMatchObject({ charity_state: 'expired', ended_reason: null });
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
