import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '@shared/query/http';
import {
  getDonationSourceKeysPage,
  getDonationSourcesPage,
  getManagedDonationKeysPage,
  getManagedDonationsPage,
  normalizeDonationSourceKeysPage,
  normalizeDonationSourcesPage,
  normalizeManagedDonationKeysPage,
  normalizeManagedDonationsPage,
} from './donationPages';

const SOURCE_KEY = `dsg_${'_'.repeat(42)}8`;
const RULE_IDS = [
  `qlr_${'A'.repeat(21)}Q`,
  `qlr_${'A'.repeat(21)}g`,
  `qlr_${'A'.repeat(21)}w`,
  `qlr_${'A'.repeat(21)}A`,
];
const CHANNEL_ID = `mch_${'A'.repeat(21)}Q`;
const LARGE_AMOUNT = '170141183460469231731687303715884105.727';
const U128_MAX = '340282366920938463463374607431768211455';

function jsonResponse(value: unknown, status = 200): Response {
  return new Response(JSON.stringify(value), {
    status,
    headers: { 'content-type': 'application/json' },
  });
}

function handling(overrides: Record<string, unknown> = {}) {
  return {
    state: 'pending',
    revision: '1',
    processed_at: null,
    processed_by_role: null,
    closed_at: null,
    closed_reason: null,
    ...overrides,
  };
}

function logicalExpiryHandling() {
  return handling({ state: 'closed', closed_reason: 'expired' });
}

function customSource(overrides: Record<string, unknown> = {}) {
  return {
    kind: 'custom',
    connector_type: 'openai-compatible',
    base_url: 'https://charity.example.test/v1',
    ...overrides,
  };
}

function mainstreamSource(overrides: Record<string, unknown> = {}) {
  return {
    kind: 'mainstream',
    connector_type: 'anthropic-compatible',
    base_url: 'https://channel.example.test/v1',
    channel_id: CHANNEL_ID,
    name: 'Stable channel',
    ...overrides,
  };
}

function zeroStateCounts() {
  return {
    available: '0',
    pending: '0',
    disabled: '0',
    suspended: '0',
    exhausted: '0',
    expired: '0',
    ended: '0',
  };
}

function donation(overrides: Record<string, unknown> = {}) {
  return {
    id: '7',
    status: 'pending',
    revision: '1',
    description: 'Public donation description',
    review_result: null,
    created_at: 1_800_000_000,
    updated_at: 1_800_000_001,
    key_count: '1',
    state_counts: { ...zeroStateCounts(), available: '1' },
    source_count: '1',
    sources: [customSource()],
    handling: handling(),
    reviewer: null,
    owner: null,
    ...overrides,
  };
}

function approved(overrides: Record<string, unknown> = {}) {
  return donation({
    status: 'approved',
    review_result: { decision: 'approve', reason: '', reviewed_at: 1_800_000_002 },
    ...overrides,
  });
}

function rule(id = RULE_IDS[0], overrides: Record<string, unknown> = {}) {
  return {
    id,
    mode: 'reset',
    interval: 'day',
    alignment: 'first_success',
    time_zone: 'UTC',
    week_starts_on: null,
    metric: 'calls',
    limit: '100',
    used: '0',
    reserved: '0',
    remaining: '100',
    state: 'available',
    period_start: null,
    period_end: null,
    next_transition_at: null,
    ...overrides,
  };
}

function keySummary(overrides: Record<string, unknown> = {}) {
  return {
    id: '11',
    key_id: '11',
    donation_id: '7',
    donation_revision: '2',
    endpoint_key_id: '21',
    display_head: 'abcdefghijklmnop',
    display_tail: 'qrstuvwxyzABCDEF',
    safe_source: customSource(),
    physical_enabled: true,
    charity_state: 'available',
    limits: { price: LARGE_AMOUNT, calls: '1000', tokens: '2000' },
    usage: {
      price_used: '0',
      price_inflight: '0',
      calls_used: '0',
      calls_inflight: '0',
      tokens_used: '0',
      tokens_inflight: '0',
    },
    token_reserve: 0,
    expires_at: 1_900_000_000,
    authorized_expires_at: 1_900_000_100,
    streak: { generation: '1', count: '0', failure_disabled: false },
    ended_reason: null,
    safe_note: 'Reviewer-only safe note',
    max_concurrency: 4,
    max_rpm: 60,
    binding_count: '0',
    idle: true,
    rule_count: '1',
    rules: [rule()],
    handling: handling(),
    ...overrides,
  };
}

function sourceSummary(overrides: Record<string, unknown> = {}) {
  return {
    source_key: SOURCE_KEY,
    safe_source: customSource(),
    donation_count: '2',
    key_count: '3',
    usable_key_count: '2',
    pending_donation_count: '1',
    ...overrides,
  };
}

function page(data: unknown[], pagination: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    data,
    next_cursor: null,
    pagination: {
      page: '1',
      page_size: 20,
      total_items: String(data.length),
      total_pages: '1',
      ...pagination,
    },
  };
}

afterEach(() => vi.unstubAllGlobals());

describe('donation page operations', () => {
  it('uses the admin donation path, encodes all filters, and forwards AbortSignal', async () => {
    const signal = new AbortController().signal;
    const fetchMock = vi.fn<typeof fetch>(async () =>
      jsonResponse(
        page(
          [
            approved({
              handling: handling({
                state: 'processed',
                processed_at: 1_800_000_003,
                processed_by_role: 'admin',
              }),
              reviewer: { user_id: '2', role: 'admin' },
              key_count: '0',
              state_counts: zeroStateCounts(),
              source_count: '0',
              sources: [],
            }),
          ],
          { page: '2', page_size: 50, total_items: '51', total_pages: '2' },
        ),
      ),
    );
    vi.stubGlobal('fetch', fetchMock);

    const result = await getManagedDonationsPage(
      'admin',
      { status: 'approved', handling: 'processed', q: '名字 +/?' },
      '2',
      50,
      signal,
    );

    expect(String(fetchMock.mock.calls[0]?.[0])).toBe(
      '/admin/api/donations?status=approved&handling=processed&q=%E5%90%8D%E5%AD%97+%2B%2F%3F&page=2&page_size=50',
    );
    expect(fetchMock.mock.calls[0]?.[1]?.signal).toBe(signal);
    expect(result.pagination).toMatchObject({ page: '2', page_size: 50, total_items: '51' });
    expect(result.data[0]).toMatchObject({ status: 'approved', owner: null });
  });

  it('preserves the same owner identity fields for admin and steward pages', () => {
    const owner = { user_id: '7', discord_id: 'donor-discord', display_name: 'Donor' };
    const admin = normalizeManagedDonationsPage(page([donation({ owner })]), 'admin', {}, '1', 20);
    const steward = normalizeManagedDonationsPage(
      page([donation({ owner })]),
      'steward',
      {},
      '1',
      20,
    );
    expect(admin.data[0]?.owner).toEqual(owner);
    expect(steward.data[0]?.owner).toEqual(owner);
    expect(() =>
      normalizeManagedDonationsPage(
        page([donation({ owner: { ...owner, email: 'private' } })]),
        'steward',
        {},
        '1',
        20,
      ),
    ).toThrow(ApiError);
  });

  it('uses the steward key path, preserves wide decimal fields, and matches donation id', async () => {
    const donationID = '123456789012345678901234567890';
    const keyID = '987654321098765432109876543210';
    const signal = new AbortController().signal;
    const fetchMock = vi.fn<typeof fetch>(async () =>
      jsonResponse(
        page(
          [
            keySummary({
              id: keyID,
              key_id: keyID,
              donation_id: donationID,
              donation_revision: '340282366920938463463374607431768211455',
              limits: { price: LARGE_AMOUNT, calls: null, tokens: null },
            }),
          ],
          { page: '3', page_size: 100, total_items: '201', total_pages: '3' },
        ),
      ),
    );
    vi.stubGlobal('fetch', fetchMock);

    const result = await getManagedDonationKeysPage('steward', donationID, '3', 100, signal);

    expect(String(fetchMock.mock.calls[0]?.[0])).toBe(
      `/api/steward/donations/${donationID}/keys?page=3&page_size=100`,
    );
    expect(fetchMock.mock.calls[0]?.[1]?.signal).toBe(signal);
    expect(result.data[0]?.id).toBe(keyID);
    expect(result.data[0]?.key_id).toBe(keyID);
    expect(result.data[0]?.donation_id).toBe(donationID);
    expect(result.data[0]?.donation_revision).toBe('340282366920938463463374607431768211455');
  });

  it('uses both role-specific source paths and encodes q, scope, and handling', async () => {
    const signal = new AbortController().signal;
    const fetchMock = vi.fn<typeof fetch>(async () => jsonResponse(page([sourceSummary()])));
    vi.stubGlobal('fetch', fetchMock);

    await getDonationSourcesPage(
      'admin',
      { q: '渠道 +/?', scope: 'all', handling: 'pending' },
      '1',
      20,
      signal,
    );
    await getDonationSourcesPage(
      'steward',
      { q: '渠道 +/?', scope: 'all', handling: 'pending' },
      '1',
      20,
      signal,
    );

    expect(String(fetchMock.mock.calls[0]?.[0])).toBe(
      '/admin/api/donation-sources?q=%E6%B8%A0%E9%81%93+%2B%2F%3F&scope=all&handling=pending&page=1&page_size=20',
    );
    expect(String(fetchMock.mock.calls[1]?.[0])).toBe(
      '/api/steward/donation-sources?q=%E6%B8%A0%E9%81%93+%2B%2F%3F&scope=all&handling=pending&page=1&page_size=20',
    );
    expect(fetchMock.mock.calls[0]?.[1]?.signal).toBe(signal);
    expect(fetchMock.mock.calls[1]?.[1]?.signal).toBe(signal);
  });

  it('uses the source key path, accepts keys from different donations, and applies idle filter', async () => {
    const signal = new AbortController().signal;
    const fetchMock = vi.fn<typeof fetch>(async () =>
      jsonResponse(
        page(
          [
            keySummary({
              id: '11',
              key_id: '11',
              donation_id: '7',
              idle: false,
              binding_count: '1',
            }),
            keySummary({
              id: '12',
              key_id: '12',
              donation_id: '8',
              idle: false,
              binding_count: '1',
            }),
          ],
          { total_items: '2' },
        ),
      ),
    );
    vi.stubGlobal('fetch', fetchMock);

    const result = await getDonationSourceKeysPage(
      'admin',
      SOURCE_KEY,
      { q: 'source', scope: 'active', handling: 'pending', idle: 'no' },
      '1',
      20,
      signal,
    );

    expect(String(fetchMock.mock.calls[0]?.[0])).toBe(
      `/admin/api/donation-sources/${SOURCE_KEY}/keys?q=source&scope=active&handling=pending&idle=no&page=1&page_size=20`,
    );
    expect(fetchMock.mock.calls[0]?.[1]?.signal).toBe(signal);
    expect(result.data.map((item) => item.donation_id)).toEqual(['7', '8']);
  });

  it('accepts a valid clamped final page and wide source counters', () => {
    const result = normalizeDonationSourcesPage(
      page(
        [
          sourceSummary({
            donation_count: U128_MAX,
            key_count: U128_MAX,
            usable_key_count: U128_MAX,
            pending_donation_count: U128_MAX,
          }),
        ],
        { page: '2', page_size: 20, total_items: '21', total_pages: '2' },
      ),
      'admin',
      {},
      '99',
      20,
    );
    expect(result.pagination.page).toBe('2');
    expect(result.data[0]?.key_count).toBe(U128_MAX);
  });

  it('accepts the logical expiry handling shape only in the new summary pages', () => {
    const expired = approved({
      status: 'expired',
      handling: logicalExpiryHandling(),
      key_count: '0',
      state_counts: zeroStateCounts(),
      source_count: '0',
      sources: [],
    });
    const result = normalizeManagedDonationsPage(page([expired]), 'admin', {}, '1', 20);
    expect(result.data[0]?.handling).toMatchObject({
      state: 'closed',
      closed_at: null,
      closed_reason: 'expired',
    });

    const key = keySummary({ charity_state: 'expired', handling: logicalExpiryHandling() });
    const keyResult = normalizeManagedDonationKeysPage(page([key]), '7', '1', 20);
    expect(keyResult.data[0]?.handling.closed_at).toBeNull();
    expect(() =>
      normalizeManagedDonationsPage(
        { ...page([expired]), data: [{ ...expired, status: 'approved' }] },
        'admin',
        {},
        '1',
        20,
      ),
    ).toThrow(ApiError);
  });

  it('keeps rule_count above the visible three-rule cap and retains limited priority', () => {
    const result = normalizeManagedDonationKeysPage(
      page([
        keySummary({
          rule_count: '4',
          rules: [
            rule(RULE_IDS[0], { state: 'limited', remaining: '0' }),
            rule(RULE_IDS[1]),
            rule(RULE_IDS[2]),
          ],
        }),
      ]),
      '7',
      '1',
      20,
    );
    expect(result.data[0]?.rule_count).toBe('4');
    expect(result.data[0]?.rules).toHaveLength(3);
    expect(result.data[0]?.rules[0]?.state).toBe('limited');
  });

  it('rejects private fields, duplicate IDs, cursor mixing, bad counts, and wrong windows', () => {
    expect(() =>
      normalizeManagedDonationsPage(
        page([
          donation({
            sources: [mainstreamSource({ channel_revision: '2', category: 'subscription' })],
          }),
        ]),
        'admin',
        {},
        '1',
        20,
      ),
    ).toThrow(ApiError);
    expect(() =>
      normalizeManagedDonationsPage(page([donation({ keys: [] })]), 'admin', {}, '1', 20),
    ).toThrow(ApiError);
    expect(() =>
      normalizeManagedDonationKeysPage(
        page([
          keySummary({
            safe_source: mainstreamSource({ channel_revision: '2', category: 'subscription' }),
          }),
        ]),
        '7',
        '1',
        20,
      ),
    ).toThrow(ApiError);
    expect(() =>
      normalizeManagedDonationsPage(page([donation(), donation()]), 'admin', {}, '1', 20),
    ).toThrow(ApiError);
    expect(() =>
      normalizeManagedDonationsPage(
        { ...page([donation()]), next_cursor: 'opaque' },
        'admin',
        {},
        '1',
        20,
      ),
    ).toThrow(ApiError);
    expect(() =>
      normalizeManagedDonationsPage(
        page([donation({ state_counts: { ...zeroStateCounts(), available: '2' } })]),
        'admin',
        {},
        '1',
        20,
      ),
    ).toThrow(ApiError);
    expect(() =>
      normalizeDonationSourcesPage(
        page([sourceSummary({ donation_count: '4', key_count: '3' })]),
        'admin',
        {},
        '1',
        20,
      ),
    ).toThrow(ApiError);
    expect(() =>
      normalizeManagedDonationsPage(
        page([donation()], { page: '1', page_size: 20, total_items: '21', total_pages: '2' }),
        'admin',
        {},
        '2',
        20,
      ),
    ).toThrow(ApiError);
    expect(() =>
      normalizeManagedDonationKeysPage(
        page([
          keySummary({
            rule_count: '2',
            rules: [rule(RULE_IDS[0]), rule(RULE_IDS[1], { state: 'limited', remaining: '0' })],
          }),
        ]),
        '7',
        '1',
        20,
      ),
    ).toThrow(ApiError);
  });

  it('rejects malformed filters, page windows, roles, source keys, and private source filters before fetch', () => {
    const fetchMock = vi.fn<typeof fetch>(async () => jsonResponse(page([])));
    vi.stubGlobal('fetch', fetchMock);
    const validSourceKey = SOURCE_KEY;

    expect(() => getManagedDonationsPage('admin', { cursor: 'bad' } as never, '1', 20)).toThrow(
      ApiError,
    );
    expect(() => getManagedDonationsPage('admin', {}, '01', 20)).toThrow(ApiError);
    expect(() => getManagedDonationsPage('admin', {}, '1', 40 as never)).toThrow(ApiError);
    expect(() => getManagedDonationsPage('owner' as never, {}, '1', 20)).toThrow(ApiError);
    expect(() => getManagedDonationsPage('admin', { q: 'x'.repeat(513) }, '1', 20)).toThrow(
      ApiError,
    );
    expect(() => getDonationSourcesPage('admin', { idle: 'yes' } as never, '1', 20)).toThrow(
      ApiError,
    );
    expect(() => getDonationSourcesPage('admin', { scope: '' } as never, '1', 20)).toThrow(
      ApiError,
    );
    expect(() =>
      getDonationSourceKeysPage('admin', validSourceKey, { idle: '' } as never, '1', 20),
    ).toThrow(ApiError);
    expect(() => getDonationSourceKeysPage('admin', 'mch_' + 'A'.repeat(22), {}, '1', 20)).toThrow(
      ApiError,
    );
    expect(() => getDonationSourceKeysPage('admin', `${validSourceKey}=`, {}, '1', 20)).toThrow(
      ApiError,
    );
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('rejects source key rows that violate idle and pending handling filters', () => {
    expect(() =>
      normalizeDonationSourceKeysPage(
        page([keySummary({ idle: true })]),
        SOURCE_KEY,
        { idle: 'no' },
        '1',
        20,
      ),
    ).toThrow(ApiError);
    expect(() =>
      normalizeDonationSourceKeysPage(
        page([
          keySummary({
            handling: handling({ state: 'processed', processed_at: 1, processed_by_role: 'admin' }),
          }),
        ]),
        SOURCE_KEY,
        { handling: 'pending' },
        '1',
        20,
      ),
    ).toThrow(ApiError);
  });

  it('preserves ordinary API errors from the shared HTTP boundary', async () => {
    const fetchMock = vi.fn<typeof fetch>(async () =>
      jsonResponse({ error: { code: 'forbidden', message: 'not allowed' } }, 403),
    );
    vi.stubGlobal('fetch', fetchMock);

    await expect(getDonationSourcesPage('steward', {}, '1', 20)).rejects.toMatchObject({
      code: 'forbidden',
      status: 403,
    });
  });
});
