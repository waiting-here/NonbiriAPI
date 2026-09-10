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
  handling: {
    state: 'pending',
    revision: '1',
    processed_at: null,
    processed_by_role: null,
    closed_at: null,
    closed_reason: null,
  },
  description: 'Donor-safe description',
  review_result: null,
  keys: [],
  reviewer: null,
  created_at: 1,
  updated_at: 1,
};

describe.each([normalizeAdminDonation, normalizeStewardDonation])(
  'terminal donation review',
  (normalize) => {
    it.each(['expired', 'deleted'])(
      'preserves absent, automatic and manual reviews for %s',
      (status) => {
        const review = { decision: 'approve', reason: '', reviewed_at: 1 };
        for (const fields of [
          { reviewer: null, review_result: null },
          { reviewer: null, review_result: review },
          {
            reviewer: { role: 'admin', user_id: '2' },
            review_result: { ...review, reason: 'accepted' },
          },
          { reviewer: { role: 'steward', user_id: null }, review_result: review },
        ]) {
          const out = normalize({ ...common, owner: null, status, ...fields });
          expect(out.review_result).toEqual(fields.review_result);
          expect(out.reviewer).toEqual(fields.reviewer);
        }
      },
    );

    it.each(['pending', 'approved', 'rejected', 'expired', 'deleted'])(
      'rejects contradictory %s reviews',
      (status) => {
        const review = { decision: 'reject', reason: 'rejected', reviewed_at: 1 };
        expect(() => normalize({ ...common, owner: null, status, review_result: review })).toThrow(
          /review/i,
        );
        expect(() =>
          normalize({ ...common, owner: null, status, reviewer: { role: 'admin', user_id: '2' } }),
        ).toThrow(/review/i);
        if (status !== 'rejected') {
          expect(() =>
            normalize({
              ...common,
              owner: null,
              status,
              review_result: review,
              reviewer: { role: 'admin', user_id: '2' },
            }),
          ).toThrow(/review/i);
        }
      },
    );
  },
);

const model = {
  id: '1',
  provider: 'provider',
  model: 'model',
  full_name: '[公益]provider/model',
  enabled: true,
  allowed_levels: [1, 2, 3, 4, 5],
  public_description: '',
  token_reserve_credits: null,
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
  binding_count: '0',
  idle: true,
  endpoint_key_id: '21',
  display_head: 'A'.repeat(16),
  display_tail: 'Z'.repeat(16),
  safe_source: {
    kind: 'mainstream',
    channel_id: `mch_${'A'.repeat(22)}`,
    name: 'Frozen channel',
    channel_revision: '1',
    category: 'subscription',
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
  it.each(['admin', 'steward', 'level5'])(
    'accepts an optional manual review reason from %s',
    (role) => {
      const reviewed = {
        ...common,
        status: 'approved',
        review_result: { decision: 'approve', reason: '', reviewed_at: 2 },
        reviewer: { user_id: '2', role },
      };
      const expected = role === 'level5' ? 'steward' : role;
      expect(normalizeAdminDonation({ ...reviewed, owner: null }).reviewer?.role).toBe(expected);
      expect(
        normalizeStewardDonation({
          ...reviewed,
          owner: { user_id: '2', discord_id: 'steward-discord', display_name: 'Owner' },
        }).reviewer?.role,
      ).toBe(expected);
      expect(() =>
        normalizeAdminDonation({ ...reviewed, owner: null, review_result: null }),
      ).toThrow(/review/i);
    },
  );

  it('accepts deidentified and full owner projections in management views', () => {
    expect(normalizeAdminDonation({ ...common, owner: null }).owner).toBeNull();
    expect(
      normalizeStewardDonation({
        ...common,
        owner: { user_id: '2', discord_id: 'steward-discord', display_name: 'Owner' },
      }).owner,
    ).toEqual({ user_id: '2', discord_id: 'steward-discord', display_name: 'Owner' });
    expect(normalizeStewardDonation({ ...common, owner: null }).owner).toBeNull();
  });

  it('keeps admin and steward projections closed and rejects secret-bearing additions', () => {
    expect(() =>
      normalizeAdminDonation({ ...common, owner: null, secret: 'sk-never-project' }),
    ).toThrow(/invalid administrator donation/i);
    expect(
      normalizeStewardDonation({
        ...common,
        owner: { user_id: '2', display_name: 'Owner', discord_id: 'private' },
      }).owner?.discord_id,
    ).toBe('private');
    expect(() =>
      normalizeStewardDonation({
        ...common,
        owner: { user_id: '2', display_name: 'Owner', discord_id: 'private', email: 'secret' },
      }),
    ).toThrow(/invalid steward donation owner/i);
    expect(() =>
      normalizeAdminDonation({ ...common, owner: { user_id: '2', display_name: 'Owner' } }),
    ).toThrow(/invalid administrator donation owner/i);
    expect(() => normalizeAdminDonation({ ...common, owner: null, expires_at: null })).toThrow(
      /invalid administrator donation/i,
    );
  });

  it('keeps full management provenance and enforces the donor expiry ceiling', () => {
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

    const stewardOwner = { user_id: '2', discord_id: 'steward-discord', display_name: 'Owner' };
    expect(
      normalizeStewardDonation({ ...common, keys: [managedKey], owner: stewardOwner }).keys[0]
        .safe_source,
    ).toMatchObject({ channel_revision: '1', category: 'subscription' });
    expect(
      normalizeStewardDonation({
        ...common,
        keys: [{ ...managedKey, safe_source: adminSource }],
        owner: stewardOwner,
      }).keys[0]?.safe_source,
    ).toMatchObject({ channel_revision: '3', category: 'subscription' });
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

  it('preserves multiple steward keys and an absent endpoint snapshot', () => {
    const result = normalizeStewardDonation({
      ...common,
      keys: [
        managedKey,
        {
          ...managedKey,
          id: '12',
          endpoint_key_id: null,
          safe_source: {
            kind: 'custom',
            connector_type: 'anthropic-compatible',
            base_url: 'https://custom.example.test/v1',
          },
        },
      ],
      owner: { user_id: '2', discord_id: null, display_name: 'Owner' },
    });
    expect(result.keys.map((key) => [key.id, key.endpoint_key_id])).toEqual([
      ['11', '21'],
      ['12', null],
    ]);
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
      owner: { user_id: '2', discord_id: 'steward-discord', display_name: 'Owner' },
    });
    expect(result.keys[0]).toMatchObject({ charity_state: 'expired', ended_reason: null });
  });
});

describe('charity model wire', () => {
  it('validates sorted level access and canonicalizes public description line endings', () => {
    expect(
      normalizeAdminCharityModel({
        ...model,
        allowed_levels: [1, 3, 5],
        public_description: 'First\r\nSecond\t<b>literal</b>',
      }),
    ).toMatchObject({
      allowed_levels: [1, 3, 5],
      public_description: 'First\nSecond\t<b>literal</b>',
    });
    for (const allowed_levels of [[0], [6], [1, 1], [2, 1]]) {
      expect(() => normalizeAdminCharityModel({ ...model, allowed_levels })).toThrow(
        /allowed levels/i,
      );
    }
    expect(() => normalizeAdminCharityModel({ ...model, allowed_levels: null })).toThrow(
      /allowed levels/i,
    );
    expect(() => normalizeAdminCharityModel({ ...model, allowed_levels: undefined })).toThrow(
      /allowed levels/i,
    );
  });

  it('enforces public description code point, byte, and control character limits', () => {
    expect(
      normalizeAdminCharityModel({ ...model, public_description: '😀'.repeat(1_024) })
        .public_description,
    ).toHaveLength(2_048);
    expect(() =>
      normalizeAdminCharityModel({ ...model, public_description: 'a'.repeat(1_025) }),
    ).toThrow(/public description/i);
    expect(() =>
      normalizeAdminCharityModel({ ...model, public_description: '😀'.repeat(1_025) }),
    ).toThrow(/public description/i);
    for (const control of ['\0', '\r', '\u000b', '\u007f', '\u0085']) {
      expect(() =>
        normalizeAdminCharityModel({ ...model, public_description: `safe${control}text` }),
      ).toThrow(/public description/i);
    }
    expect(() => normalizeAdminCharityModel({ ...model, public_description: null })).toThrow(
      /public description/i,
    );
    expect(() => normalizeAdminCharityModel({ ...model, public_description: undefined })).toThrow(
      /public description/i,
    );
  });

  it('rejects private model fields while accepting an empty public description', () => {
    expect(normalizeStewardCharityModel(model).public_description).toBe('');
    expect(() => normalizeAdminCharityModel({ ...model, secret: 'upstream-key' })).toThrow(
      /invalid administrator charity model/i,
    );
  });

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
    expect(() => normalizeAdminCharityModel({ ...model, full_name: 'provider/model' })).toThrow(
      /full name/i,
    );
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

  it('normalizes an optional per-token reserve without losing decimal precision', () => {
    for (const token_reserve_credits of [
      '9000000000000.001',
      '0',
      '0.000',
      '1.000',
      '1e3',
      '-1',
      1,
      '9000000000001',
    ]) {
      expect(() => normalizeAdminCharityModel({ ...model, token_reserve_credits })).toThrow(
        /token reserve credits/i,
      );
    }
    for (const token_reserve_credits of ['0.001', '0.01', '1.234']) {
      expect(
        normalizeAdminCharityModel({ ...model, token_reserve_credits }).token_reserve_credits,
      ).toBe(token_reserve_credits);
    }
    expect(
      normalizeAdminCharityModel({ ...model, token_reserve_credits: '9000000000000' })
        .token_reserve_credits,
    ).toBe('9000000000000');
    expect(
      normalizeStewardCharityModel({ ...model, token_reserve_credits: '1.234' })
        .token_reserve_credits,
    ).toBe('1.234');
    const legacyModel = Object.fromEntries(
      Object.entries(model).filter(([key]) => key !== 'token_reserve_credits'),
    );
    expect(normalizeAdminCharityModel(legacyModel).token_reserve_credits).toBeNull();
  });

  it('accepts binding revision zero from the bindings API', async () => {
    const fetchMock = vi.fn<typeof fetch>(
      async () =>
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
