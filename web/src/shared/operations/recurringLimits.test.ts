import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '@shared/query/http';
import {
  getRecurringLimits,
  normalizeRecurringLimitsResponse,
  putRecurringLimits,
  recurringLimitsPath,
  type RecurringLimitRuleInput,
} from './recurringLimits';

const RULE_ID = `qlr_${'A'.repeat(21)}Q`;
const U128_MAX = ((1n << 128n) - 1n).toString();

function rule(overrides: Partial<Record<string, unknown>> = {}) {
  return {
    id: RULE_ID,
    mode: 'reset',
    interval: 'week',
    alignment: 'calendar',
    time_zone: 'UTC',
    week_starts_on: 1,
    metric: 'calls',
    limit: U128_MAX,
    used: '123456789012345678901234567890',
    reserved: '17',
    remaining: '0',
    state: 'limited',
    period_start: -62_167_219_200,
    period_end: 253_402_300_799,
    next_transition_at: null,
    ...overrides,
  };
}

function response(overrides: Record<string, unknown> = {}) {
  return {
    donation_id: '7',
    key_id: '8',
    donation_revision: '9',
    server_now: 1_800_000_000,
    rules: [rule()],
    ...overrides,
  };
}

function jsonResponse(value: unknown, status = 200): Response {
  return new Response(JSON.stringify(value), {
    status,
    headers: { 'content-type': 'application/json' },
  });
}

afterEach(() => vi.unstubAllGlobals());

describe('recurring limits operations', () => {
  it('uses the role-specific single-key paths and preserves exact wire strings', async () => {
    const fetchMock = vi.fn<typeof fetch>(async () => jsonResponse(response()));
    vi.stubGlobal('fetch', fetchMock);

    const value = await getRecurringLimits('owner', '7', '8');

    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(String(fetchMock.mock.calls[0]?.[0])).toBe('/api/donations/7/keys/8/recurring-limits');
    expect(value.rules[0]?.limit).toBe(U128_MAX);
    expect(value.rules[0]?.period_start).toBe(-62_167_219_200);
    expect(recurringLimitsPath('steward', '7', '8')).toBe(
      '/api/steward/donations/7/keys/8/recurring-limits',
    );
    expect(recurringLimitsPath('admin', '7', '8')).toBe(
      '/admin/api/donations/7/keys/8/recurring-limits',
    );
  });

  it('accepts the one-hour reset and sliding intervals while rejecting calendar alignment', () => {
    const slidingRuleID = `qlr_${'B'.repeat(21)}Q`;
    const normalized = normalizeRecurringLimitsResponse(
      response({
        rules: [
          rule({ interval: '1h', alignment: 'first_success', week_starts_on: null }),
          rule({
            id: slidingRuleID,
            mode: 'sliding',
            interval: '1h',
            alignment: null,
            week_starts_on: null,
            period_start: null,
            period_end: null,
          }),
        ],
      }),
    );

    expect(
      normalized.rules.map(({ interval, mode, alignment }) => [interval, mode, alignment]),
    ).toEqual([
      ['1h', 'reset', 'first_success'],
      ['1h', 'sliding', null],
    ]);
    expect(() =>
      normalizeRecurringLimitsResponse(
        response({ rules: [rule({ interval: '1h', alignment: 'calendar' })] }),
      ),
    ).toThrow(ApiError);
  });

  it('submits all eight rule fields with the captured revision and idempotency key', async () => {
    const fetchMock = vi.fn<typeof fetch>(async () =>
      jsonResponse({ donation_id: '7', key_id: '8', donation_revision: '10' }),
    );
    vi.stubGlobal('fetch', fetchMock);
    const input: RecurringLimitRuleInput = {
      id: RULE_ID,
      mode: 'sliding',
      interval: 'month',
      alignment: null,
      time_zone: 'America/Los_Angeles',
      week_starts_on: null,
      metric: 'credits',
      limit: '340282366920938463463374607431768211.455',
    };

    await putRecurringLimits(
      'steward',
      '7',
      '8',
      { expected_revision: '9', rules: [input] },
      'AAAAAAAAAAAAAAAAAAAAAA',
    );

    const init = fetchMock.mock.calls[0]?.[1];
    expect(String(fetchMock.mock.calls[0]?.[0])).toBe(
      '/api/steward/donations/7/keys/8/recurring-limits',
    );
    expect(init?.method).toBe('PUT');
    expect(new Headers(init?.headers).get('Idempotency-Key')).toBe('AAAAAAAAAAAAAAAAAAAAAA');
    expect(JSON.parse(String(init?.body))).toEqual({
      expected_revision: '9',
      rules: [input],
    });
  });

  it('rejects unknown fields, illegal combinations, and non-canonical credit values', () => {
    expect(() => normalizeRecurringLimitsResponse({ ...response(), extra: true })).toThrow(
      ApiError,
    );
    expect(() =>
      putRecurringLimits(
        'admin',
        '7',
        '8',
        {
          expected_revision: '9',
          rules: [],
          extra: true,
        } as unknown as Parameters<typeof putRecurringLimits>[3],
        'CCCCCCCCCCCCCCCCCCCCCC',
      ),
    ).toThrow(ApiError);
    expect(() =>
      normalizeRecurringLimitsResponse(response({ rules: [rule({ interval: '5h' })] })),
    ).toThrow(ApiError);
    expect(() =>
      normalizeRecurringLimitsResponse(
        response({ rules: [rule({ limit: '1.000', metric: 'credits' })] }),
      ),
    ).toThrow(ApiError);
    expect(() =>
      normalizeRecurringLimitsResponse(
        response({
          rules: [rule({ limit: '340282366920938463463374607431768211.456', metric: 'credits' })],
        }),
      ),
    ).toThrow(ApiError);
  });

  it('rejects malformed write rules before issuing a request', async () => {
    const fetchMock = vi.fn<typeof fetch>(async () => jsonResponse({}));
    vi.stubGlobal('fetch', fetchMock);
    expect(() =>
      putRecurringLimits(
        'admin',
        '7',
        '8',
        {
          expected_revision: '9',
          rules: [
            {
              id: null,
              mode: 'reset',
              interval: '5h',
              alignment: 'calendar',
              time_zone: 'UTC',
              week_starts_on: null,
              metric: 'calls',
              limit: '1',
            },
          ],
        },
        'BBBBBBBBBBBBBBBBBBBBBB',
      ),
    ).toThrow(ApiError);
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('writes a one-hour reset rule with the exact wire interval', async () => {
    const fetchMock = vi.fn<typeof fetch>(async () =>
      jsonResponse({ donation_id: '7', key_id: '8', donation_revision: '10' }),
    );
    vi.stubGlobal('fetch', fetchMock);
    const input: RecurringLimitRuleInput = {
      id: RULE_ID,
      mode: 'reset',
      interval: '1h',
      alignment: 'first_success',
      time_zone: 'UTC',
      week_starts_on: null,
      metric: 'calls',
      limit: '12',
    };

    await putRecurringLimits(
      'steward',
      '7',
      '8',
      { expected_revision: '9', rules: [input] },
      'EEEEEEEEEEEEEEEEEEEEEE',
    );

    expect(JSON.parse(String(fetchMock.mock.calls[0]?.[1]?.body))).toEqual({
      expected_revision: '9',
      rules: [input],
    });
  });

  it('does not expose a write path for key owners', () => {
    expect(() =>
      putRecurringLimits(
        'owner',
        '7',
        '8',
        { expected_revision: '9', rules: [] },
        'DDDDDDDDDDDDDDDDDDDDDD',
      ),
    ).toThrowError(
      new ApiError('forbidden', 'Recurring limits are read-only for key owners.', 403),
    );
  });
});
