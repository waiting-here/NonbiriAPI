import { describe, expect, it } from 'vitest';
import {
  amountToMilli,
  calculateWelfareAward,
  normalizeActivitiesSnapshot,
  normalizeCharityCapability,
  normalizeDonation,
  normalizeDonationKey,
  normalizeWelfareClaimResult,
} from './normalize';

export const ACTIVITY_FIXTURE = {
  master: { enabled: true, available: true, reason: 'available' },
  welfare: {
    enabled: true,
    state: 'available',
    site_day: '2026-08-31',
    threshold: '10',
    cap: '2.5',
    pool_balance: '9.999',
    claimed_today: false,
  },
  thursday: {
    enabled: true,
    state: 'open',
    server_now: 1_788_111_000,
    current: {
      period_id: 'thu_abcdefghijklmnopqrstuA',
      revision: '7',
      opens_at: 1_788_110_000,
      closes_at: 1_788_196_400,
      literature: '<b>plain text only</b> V me 50',
      entry: '50',
      per_user_limit: 3,
      pool_balance: '12345678901234567890.123',
      my_count: '1',
      my_contributed: '50',
    },
    next: null,
    last_result: null,
  },
} as const;

const DONATION_FIXTURE = {
  id: '41',
  status: 'approved',
  revision: '9',
  description: 'Existing resources only',
  review_result: { decision: 'approve', reason: 'Accepted', reviewed_at: 1_788_100_010 },
  expires_at: null,
  keys: [
    {
      id: '51',
      endpoint_key_id: '61',
      display_head: 'sk-head',
      display_tail: 'tail',
      safe_source: { base_url: 'https://api.example.test/v1', connector_type: 'openai-compatible' },
      physical_enabled: true,
      charity_state: 'available',
      limits: { price: '9000000000000', calls: null, tokens: '9000000000000000' },
      usage: {
        price_used: '0.001',
        price_inflight: '0',
        calls_used: '9007199254740993',
        calls_inflight: '0',
        tokens_used: '0',
        tokens_inflight: '0',
      },
      token_reserve: 4096,
      streak: { generation: '2', count: '0', failure_disabled: false },
      ended_reason: null,
    },
  ],
  created_at: 1_788_100_000,
  updated_at: 1_788_100_010,
} as const;

describe('economy closed-wire normalizers', () => {
  it('accepts the canonical disabled D2 snapshot including its zero server clock', () => {
    expect(
      normalizeActivitiesSnapshot({
        master: { enabled: false, available: false, reason: 'disabled' },
        welfare: {
          enabled: false,
          state: 'unavailable',
          site_day: '',
          threshold: '0',
          cap: '0',
          pool_balance: '0',
          claimed_today: false,
        },
        thursday: {
          enabled: false,
          state: 'unavailable',
          server_now: 0,
          current: null,
          next: null,
          last_result: null,
        },
      }).thursday.serverNow,
    ).toBe(0);
  });

  it('keeps all charity capability states distinct and validates model identity', () => {
    for (const state of ['feature_disabled', 'no_models', 'no_candidates'] as const) {
      expect(normalizeCharityCapability({ state, models: [], donation_intake: 'closed' })).toEqual({
        state,
        models: [],
        donationIntake: 'closed',
      });
    }
    expect(
      normalizeCharityCapability({ state: 'no_models', models: [], donation_intake: 'open' })
        .donationIntake,
    ).toBe('open');
    expect(
      normalizeCharityCapability({
        state: 'available',
        donation_intake: 'open',
        models: [
          { id: '1', provider: 'provider', model: 'model', full_name: '[公益]provider/model' },
        ],
      }).models[0].fullName,
    ).toBe('[公益]provider/model');
    expect(() =>
      normalizeCharityCapability({ state: 'available', models: [], donation_intake: 'open' }),
    ).toThrow();
    expect(() =>
      normalizeCharityCapability({
        state: 'available',
        donation_intake: 'open',
        models: [{ id: '1', provider: 'provider', model: 'model', full_name: 'provider/model' }],
      }),
    ).toThrow();
    expect(() => normalizeCharityCapability({ state: 'no_models', models: [] })).toThrow();
    expect(() =>
      normalizeCharityCapability({
        state: 'no_models',
        models: [],
        donation_intake: 'unknown',
      }),
    ).toThrow();
    expect(() =>
      normalizeCharityCapability({
        state: 'feature_disabled',
        models: [],
        donation_intake: 'open',
      }),
    ).toThrow();
  });

  it('keeps credit amounts and U128 counters exact without floating point', () => {
    expect(amountToMilli('0.001')).toBe(1n);
    expect(amountToMilli('340282366920938463463374607431768211.455')).toBe(
      340_282_366_920_938_463_463_374_607_431_768_211_455n,
    );
    const donation = normalizeDonation(DONATION_FIXTURE);
    expect(donation.keys[0].limits.price).toBe('9000000000000');
    expect(donation.keys[0].limits.calls).toBeNull();
    expect(donation.keys[0].limits.tokens).toBe('9000000000000000');
    expect(donation.keys[0].usage.callsUsed).toBe('9007199254740993');
  });

  it('computes welfare floor and zero awards in integer milli-credits', () => {
    const snapshot = normalizeActivitiesSnapshot(ACTIVITY_FIXTURE);
    expect(calculateWelfareAward(snapshot.welfare)).toBe('0.999');
    const zero = normalizeActivitiesSnapshot({
      ...ACTIVITY_FIXTURE,
      welfare: { ...ACTIVITY_FIXTURE.welfare, state: 'empty', pool_balance: '0.009' },
    });
    expect(calculateWelfareAward(zero.welfare)).toBe('0');
  });

  it('accepts a claimed day hidden by an unavailable master overlay', () => {
    const snapshot = normalizeActivitiesSnapshot({
      ...ACTIVITY_FIXTURE,
      master: { enabled: false, available: false, reason: 'disabled' },
      welfare: {
        ...ACTIVITY_FIXTURE.welfare,
        enabled: true,
        state: 'unavailable',
        claimed_today: true,
      },
      thursday: { ...ACTIVITY_FIXTURE.thursday, state: 'unavailable' },
    });
    expect(snapshot.welfare.claimedToday).toBe(true);
  });

  it('accepts every closed Welfare and Thursday presentation state', () => {
    const welfareCases = [
      { ...ACTIVITY_FIXTURE.welfare, enabled: false, state: 'unavailable' },
      { ...ACTIVITY_FIXTURE.welfare, state: 'available' },
      { ...ACTIVITY_FIXTURE.welfare, state: 'claimed', claimed_today: true },
      { ...ACTIVITY_FIXTURE.welfare, state: 'ineligible' },
      { ...ACTIVITY_FIXTURE.welfare, state: 'empty', pool_balance: '0.009' },
      {
        ...ACTIVITY_FIXTURE.welfare,
        state: 'configuration_error',
        site_day: '',
        claimed_today: false,
      },
    ] as const;
    expect(
      welfareCases.map(
        (welfare) => normalizeActivitiesSnapshot({ ...ACTIVITY_FIXTURE, welfare }).welfare.state,
      ),
    ).toEqual([
      'unavailable',
      'available',
      'claimed',
      'ineligible',
      'empty',
      'configuration_error',
    ]);

    const next = {
      period_id: 'thu_abcdefghijklmnopqrstuQ',
      opens_at: ACTIVITY_FIXTURE.thursday.server_now + 3_600,
      pool_balance: '80',
    };
    const lastResult = {
      period_id: 'thu_abcdefghijklmnopqrstug',
      my_count: '2',
      my_contributed: '100',
      payout: '125',
      unpaid_reason: null,
    };
    const thursdayCases = [
      { ...ACTIVITY_FIXTURE.thursday, enabled: false, state: 'unavailable' },
      {
        ...ACTIVITY_FIXTURE.thursday,
        state: 'not_open',
        current: null,
        next,
        last_result: null,
      },
      ACTIVITY_FIXTURE.thursday,
      {
        ...ACTIVITY_FIXTURE.thursday,
        state: 'settling',
        server_now: ACTIVITY_FIXTURE.thursday.current.closes_at,
      },
      {
        ...ACTIVITY_FIXTURE.thursday,
        state: 'ended',
        current: null,
        last_result: lastResult,
      },
      {
        ...ACTIVITY_FIXTURE.thursday,
        state: 'configuration_error',
        current: null,
        next: null,
        last_result: null,
      },
    ] as const;
    expect(
      thursdayCases.map(
        (thursday) => normalizeActivitiesSnapshot({ ...ACTIVITY_FIXTURE, thursday }).thursday.state,
      ),
    ).toEqual(['unavailable', 'not_open', 'open', 'settling', 'ended', 'configuration_error']);
  });

  it('rejects non-canonical money and hidden internal fields', () => {
    for (const value of ['01', '+1', '-1', '1.0', '1.0000', '1e3', ' 1', '1 ']) {
      expect(() => amountToMilli(value), value).toThrow();
    }
    expect(() =>
      normalizeDonation({
        ...DONATION_FIXTURE,
        keys: [{ ...DONATION_FIXTURE.keys[0], raw_secret: 'synthetic-secret-must-never-cross' }],
      }),
    ).toThrow();
    expect(() =>
      normalizeActivitiesSnapshot({
        ...ACTIVITY_FIXTURE,
        thursday: { ...ACTIVITY_FIXTURE.thursday, participant_count: '3' },
      }),
    ).toThrow();
    expect(() =>
      normalizeActivitiesSnapshot({
        ...ACTIVITY_FIXTURE,
        welfare: { ...ACTIVITY_FIXTURE.welfare, source_events: [] },
      }),
    ).toThrow();
  });

  it('treats Thursday literature as bounded plain text data', () => {
    const snapshot = normalizeActivitiesSnapshot(ACTIVITY_FIXTURE);
    expect(snapshot.thursday.current?.literature).toBe('<b>plain text only</b> V me 50');
    expect(() =>
      normalizeActivitiesSnapshot({
        ...ACTIVITY_FIXTURE,
        thursday: {
          ...ACTIVITY_FIXTURE.thursday,
          current: { ...ACTIVITY_FIXTURE.thursday.current, literature: 'bad\ntext' },
        },
      }),
    ).toThrow();
  });

  it('accepts signed SM128 wallet balances only on signed response fields', () => {
    expect(
      normalizeWelfareClaimResult({
        awarded: '0.001',
        balance: '-12.345',
        pool_balance: '100',
        site_day: '2026-08-31',
      }).balance,
    ).toBe('-12.345');
    expect(() =>
      normalizeWelfareClaimResult({
        awarded: '0.001',
        balance: '-0.000',
        pool_balance: '100',
        site_day: '2026-08-31',
      }),
    ).toThrow();
  });

  it('preserves every Donation state and the NULL, zero, and maximum quota distinctions', () => {
    const terminalKey = {
      ...DONATION_FIXTURE.keys[0],
      charity_state: 'ended',
      ended_reason: 'withdrawn',
    };
    const cases = [
      {
        ...DONATION_FIXTURE,
        status: 'pending',
        review_result: null,
        expires_at: null,
        keys: [
          {
            ...DONATION_FIXTURE.keys[0],
            charity_state: 'pending',
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
            streak: { generation: '1', count: '0', failure_disabled: false },
          },
        ],
      },
      DONATION_FIXTURE,
      {
        ...DONATION_FIXTURE,
        status: 'rejected',
        review_result: { ...DONATION_FIXTURE.review_result, decision: 'reject' },
        expires_at: null,
        keys: [{ ...terminalKey, ended_reason: null }],
      },
      {
        ...DONATION_FIXTURE,
        status: 'deleted',
        review_result: null,
        expires_at: null,
        keys: [terminalKey],
      },
      {
        ...DONATION_FIXTURE,
        status: 'expired',
        expires_at: 1,
        keys: [{ ...terminalKey, charity_state: 'expired', ended_reason: 'expired' }],
      },
    ] as const;
    expect(cases.map((value) => normalizeDonation(value).status)).toEqual([
      'pending',
      'approved',
      'rejected',
      'deleted',
      'expired',
    ]);

    const exhausted = normalizeDonation({
      ...DONATION_FIXTURE,
      keys: [
        {
          ...DONATION_FIXTURE.keys[0],
          charity_state: 'exhausted',
          limits: { price: '0', calls: '0', tokens: null },
          usage: {
            price_used: '0',
            price_inflight: '0',
            calls_used: '0',
            calls_inflight: '0',
            tokens_used: '340282366920938463463374607431768211455',
            tokens_inflight: '0',
          },
        },
      ],
    });
    expect(exhausted.keys[0].limits).toEqual({ price: '0', calls: '0', tokens: null });
    expect(exhausted.keys[0].usage.tokensUsed).toBe('340282366920938463463374607431768211455');

    // The read projection can observe the exclusive expiry before the
    // lifecycle worker materializes the donation's terminal status.
    const overdue = normalizeDonation({
      ...DONATION_FIXTURE,
      review_result: { ...DONATION_FIXTURE.review_result, reviewed_at: 0 },
      expires_at: 0,
      keys: [{ ...terminalKey, charity_state: 'expired', ended_reason: 'expired' }],
      created_at: 0,
      updated_at: 0,
    });
    expect(overdue.status).toBe('approved');
    expect(overdue.expiresAt).toBe(0);
    expect(overdue.keys[0].charityState).toBe('expired');
    expect(() =>
      normalizeDonation({
        ...DONATION_FIXTURE,
        keys: [terminalKey],
      }),
    ).toThrow();
  });

  it('accepts every DonationKey state and preserves partial versus final deletion', () => {
    const base = DONATION_FIXTURE.keys[0];
    const states = [
      { ...base, charity_state: 'pending' },
      { ...base, charity_state: 'disabled', physical_enabled: false },
      {
        ...base,
        charity_state: 'disabled',
        streak: { ...base.streak, failure_disabled: true },
      },
      { ...base, charity_state: 'suspended' },
      {
        ...base,
        charity_state: 'exhausted',
        limits: { ...base.limits, calls: '0' },
        usage: { ...base.usage, calls_used: '0', calls_inflight: '0' },
      },
      { ...base, charity_state: 'expired', ended_reason: 'expired' },
      { ...base, charity_state: 'ended', ended_reason: 'member_removed' },
      base,
    ];
    expect(states.map((value) => normalizeDonationKey(value).charityState)).toEqual([
      'pending',
      'disabled',
      'disabled',
      'suspended',
      'exhausted',
      'expired',
      'ended',
      'available',
    ]);

    const removed = { ...base, id: '52', charity_state: 'ended', ended_reason: 'member_removed' };
    const partial = normalizeDonation({ ...DONATION_FIXTURE, keys: [removed, base] });
    expect(partial.status).toBe('approved');
    expect(partial.keys.map((key) => key.charityState)).toEqual(['ended', 'available']);
    const final = normalizeDonation({
      ...DONATION_FIXTURE,
      status: 'deleted',
      keys: [removed, { ...removed, id: '53' }],
    });
    expect(final.status).toBe('deleted');
    expect(final.keys.every((key) => key.charityState === 'ended')).toBe(true);
  });

  it('rejects duplicate identities, impossible state matrices, and values beyond U128', () => {
    expect(() =>
      normalizeCharityCapability({
        state: 'available',
        donation_intake: 'open',
        models: [
          { id: '1', provider: 'a', model: 'one', full_name: '[公益]a/one' },
          { id: '1', provider: 'a', model: 'two', full_name: '[公益]a/two' },
        ],
      }),
    ).toThrow();
    expect(() =>
      normalizeDonation({
        ...DONATION_FIXTURE,
        keys: [DONATION_FIXTURE.keys[0], DONATION_FIXTURE.keys[0]],
      }),
    ).toThrow();
    expect(() =>
      normalizeDonation({
        ...DONATION_FIXTURE,
        keys: [{ ...DONATION_FIXTURE.keys[0], safe_note: 'admin only' }],
      }),
    ).toThrow();
    expect(() =>
      normalizeDonation({
        ...DONATION_FIXTURE,
        keys: [{ ...DONATION_FIXTURE.keys[0], physical_enabled: false }],
      }),
    ).toThrow();
    expect(() =>
      normalizeActivitiesSnapshot({
        ...ACTIVITY_FIXTURE,
        thursday: { ...ACTIVITY_FIXTURE.thursday, current: null },
      }),
    ).toThrow();
    expect(() =>
      normalizeActivitiesSnapshot({
        ...ACTIVITY_FIXTURE,
        thursday: {
          ...ACTIVITY_FIXTURE.thursday,
          server_now: ACTIVITY_FIXTURE.thursday.current.closes_at,
        },
      }),
    ).toThrow();
    expect(
      normalizeActivitiesSnapshot({
        ...ACTIVITY_FIXTURE,
        thursday: {
          ...ACTIVITY_FIXTURE.thursday,
          state: 'settling',
          server_now: ACTIVITY_FIXTURE.thursday.current.closes_at,
        },
      }).thursday.state,
    ).toBe('settling');
    expect(() => amountToMilli('340282366920938463463374607431768211.456')).toThrow();
  });

  it('accepts frozen longest safe text and rejects the next rune or byte', () => {
    const provider = '界'.repeat(64);
    const model = '模'.repeat(64);
    expect(
      normalizeCharityCapability({
        state: 'available',
        donation_intake: 'open',
        models: [{ id: '1', provider, model, full_name: `[公益]${provider}/${model}` }],
      }).models[0].fullName,
    ).toBe(`[公益]${provider}/${model}`);
    expect(
      normalizeActivitiesSnapshot({
        ...ACTIVITY_FIXTURE,
        thursday: {
          ...ACTIVITY_FIXTURE.thursday,
          current: { ...ACTIVITY_FIXTURE.thursday.current, literature: '界'.repeat(1024) },
        },
      }).thursday.current?.literature,
    ).toHaveLength(1024);
    expect(
      normalizeDonation({ ...DONATION_FIXTURE, description: '界'.repeat(1024) }).description,
    ).toHaveLength(1024);
    expect(() =>
      normalizeActivitiesSnapshot({
        ...ACTIVITY_FIXTURE,
        thursday: {
          ...ACTIVITY_FIXTURE.thursday,
          current: { ...ACTIVITY_FIXTURE.thursday.current, literature: '界'.repeat(1025) },
        },
      }),
    ).toThrow();
    expect(() =>
      normalizeDonation({ ...DONATION_FIXTURE, description: '界'.repeat(1025) }),
    ).toThrow();
  });

  it('rejects impossible quota totals, zero Thursday entry, and invalid site dates', () => {
    expect(() =>
      normalizeDonation({
        ...DONATION_FIXTURE,
        keys: [
          {
            ...DONATION_FIXTURE.keys[0],
            limits: { ...DONATION_FIXTURE.keys[0].limits, calls: '1' },
            usage: { ...DONATION_FIXTURE.keys[0].usage, calls_used: '2' },
          },
        ],
      }),
    ).toThrow();
    expect(() =>
      normalizeActivitiesSnapshot({
        ...ACTIVITY_FIXTURE,
        thursday: {
          ...ACTIVITY_FIXTURE.thursday,
          current: { ...ACTIVITY_FIXTURE.thursday.current, entry: '0', my_contributed: '0' },
        },
      }),
    ).toThrow();
    expect(() =>
      normalizeActivitiesSnapshot({
        ...ACTIVITY_FIXTURE,
        welfare: { ...ACTIVITY_FIXTURE.welfare, site_day: '2026-02-30' },
      }),
    ).toThrow();
  });
});
