import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '@shared/query/http';
import {
  getOwnerDonationKeysPage,
  getOwnerDonationsPage,
  normalizeOwnerDonationKeysPage,
  normalizeOwnerDonationsPage,
} from './donationPages';

const U128_MAX_AMOUNT = '340282366920938463463374607431768211.455';
const OWNER_ID = '41';
const KEY_ID = '51';

it('retains current service failures in the owner key page', () => {
  const status = {
    use_id: '1',
    native_service: 'chat_completions',
    state: 'failure_disabled',
    failure_streak: '10',
    failure_disabled: true,
    retry_after_until: null,
    smart_backoff: null,
  };
  const result = normalizeOwnerDonationKeysPage(
    page([key({ service_statuses: [status] })]),
    OWNER_ID,
    '1',
    20,
  );
  expect(result.data[0]?.serviceStatuses).toEqual([status]);
});
const RULE_IDS = [
  `qlr_${'A'.repeat(21)}Q`,
  `qlr_${'B'.repeat(21)}Q`,
  `qlr_${'C'.repeat(21)}Q`,
  `qlr_${'D'.repeat(21)}Q`,
];

function customSource(overrides: Record<string, unknown> = {}) {
  return {
    kind: 'custom',
    connector_type: 'openai-compatible',
    base_url: 'https://api.example.test/v1',
    ...overrides,
  };
}

function summary(overrides: Record<string, unknown> = {}) {
  return {
    id: OWNER_ID,
    status: 'pending',
    revision: '1',
    description: 'owner summary',
    review_result: null,
    created_at: 1_800_000_000,
    updated_at: 1_800_000_001,
    key_count: '1',
    state_counts: {
      available: '0',
      rate_limited: '0',
      pending: '1',
      disabled: '0',
      suspended: '0',
      exhausted: '0',
      expired: '0',
      ended: '0',
    },
    source_count: '1',
    sources: [customSource()],
    ...overrides,
  };
}

function key(overrides: Record<string, unknown> = {}) {
  return {
    review: { required: false, revision: null, material_available: false },
    id: KEY_ID,
    endpoint_key_id: '61',
    display_head: 'sk-head',
    display_tail: 'tail',
    safe_source: customSource(),
    physical_enabled: true,
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
    expires_at: null,
    failure_disable_threshold: '10',
    smart_backoff: null,
    streak: { generation: '1', count: '0', failure_disabled: false },
    ended_reason: null,
    donation_id: OWNER_ID,
    key_id: KEY_ID,
    donation_revision: '1',
    rule_count: '0',
    rules: [],
    ...overrides,
  };
}

function rule(id: string, overrides: Record<string, unknown> = {}) {
  return {
    id,
    mode: 'reset',
    interval: 'week',
    alignment: 'calendar',
    time_zone: 'UTC',
    week_starts_on: 1,
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

function page(data: unknown[], overrides: Record<string, unknown> = {}) {
  return {
    data,
    next_cursor: null,
    pagination: {
      page: '1',
      page_size: 20,
      total_items: String(data.length),
      total_pages: data.length === 0 ? '1' : '1',
      ...overrides,
    },
  };
}

function jsonResponse(value: unknown, status = 200): Response {
  return new Response(JSON.stringify(value), {
    status,
    headers: { 'content-type': 'application/json' },
  });
}

afterEach(() => vi.unstubAllGlobals());

describe('owner donation numbered-page normalizers', () => {
  it.each([
    {
      total: '2',
      approved: '2',
      pending: '0',
      rejected: '0',
      withdrawn: '0',
      review_status: 'approved',
    },
    {
      total: '1',
      approved: '1',
      pending: '1',
      rejected: '0',
      withdrawn: '0',
      review_status: 'approved_pending',
    },
  ])('accepts the confirmed live pending lifecycle with $review_status uses', (useSummary) => {
    const payload = page([
      summary({
        state_counts: { ...summary().state_counts, pending: '0', available: '1' },
        use_summary: useSummary,
      }),
    ]);
    const result = normalizeOwnerDonationsPage(payload, { view: 'current' }, '1', 20).data[0]!;
    expect(result.status).toBe('pending');
    expect(result.reviewResult).toBeNull();
    expect(result.stateCounts.available).toBe('1');
    expect(result.useSummary).toEqual(useSummary);
  });
  it.each(['available', 'rate_limited', 'disabled', 'suspended', 'exhausted'] as const)(
    'accepts %s keys approved through individual uses while the lifecycle remains pending',
    (state) => {
      const reviewed = summary({
        state_counts: { ...summary().state_counts, pending: '0', [state]: '1' },
        use_summary: {
          total: '2',
          pending: '1',
          approved: '1',
          rejected: '0',
          withdrawn: '0',
          review_status: 'approved_pending',
        },
      });
      expect(
        normalizeOwnerDonationsPage(
          page([reviewed]),
          { review_status: 'approved_pending' },
          '1',
          20,
        ).data[0]?.status,
      ).toBe('pending');
      expect(
        normalizeOwnerDonationsPage(page([{ ...reviewed, use_summary: undefined }]), {}, '1', 20)
          .data[0]?.stateCounts[state],
      ).toBe('1');
    },
  );
  it('projects the exact owner summary fields into economy camelCase DTOs', () => {
    const result = normalizeOwnerDonationsPage(page([summary()]), {}, '1', 20);

    expect(result).toMatchObject({
      nextCursor: null,
      pagination: { page: '1', page_size: 20, total_items: '1', total_pages: '1' },
    });
    expect(result.data[0]).toEqual({
      id: OWNER_ID,
      ownerHiddenAt: null,
      status: 'pending',
      revision: '1',
      description: 'owner summary',
      discordPublicThanks: null,
      reviewResult: null,
      createdAt: 1_800_000_000,
      updatedAt: 1_800_000_001,
      keyCount: '1',
      stateCounts: {
        available: '0',
        rate_limited: '0',
        pending: '1',
        disabled: '0',
        suspended: '0',
        exhausted: '0',
        expired: '0',
        ended: '0',
      },
      sourceCount: '1',
      sources: [
        {
          kind: 'custom',
          connectorType: 'openai-compatible',
          baseUrl: 'https://api.example.test/v1',
        },
      ],
    });
  });

  it('accepts every supported page size and clamps an oversized requested page', () => {
    const rows = Array.from({ length: 23 }, (_, index) => summary({ id: String(index + 1) }));
    for (const pageSize of [10, 20, 50, 100] as const) {
      const actualPage = Math.ceil(rows.length / pageSize);
      const start = (actualPage - 1) * pageSize;
      const result = normalizeOwnerDonationsPage(
        page(rows.slice(start), {
          page: String(actualPage),
          page_size: pageSize,
          total_items: '23',
          total_pages: String(actualPage),
        }),
        {},
        '2147483647',
        pageSize,
      );
      expect(result.pagination.page).toBe(String(actualPage));
      expect(result.data).toHaveLength(rows.length - start);
    }
  });

  it('accepts the canonical empty page and rejects wrong response windows', () => {
    expect(
      normalizeOwnerDonationsPage(
        page([], { page_size: 50, total_items: '0', total_pages: '1' }),
        {},
        '2147483647',
        50,
      ).data,
    ).toEqual([]);
    expect(() =>
      normalizeOwnerDonationsPage(
        page([summary()], { page: '1', page_size: 50, total_items: '1', total_pages: '1' }),
        {},
        '1',
        20,
      ),
    ).toThrow(ApiError);
    expect(() =>
      normalizeOwnerDonationsPage(
        page([summary()], { page: '1', page_size: 20, total_items: '23', total_pages: '2' }),
        {},
        '9',
        20,
      ),
    ).toThrow(ApiError);
  });

  it('keeps large canonical counts without recomputing server summaries', () => {
    const largeCount = '9007199254740993';
    const result = normalizeOwnerDonationsPage(
      page([
        summary({
          key_count: largeCount,
          state_counts: {
            available: '0',
            rate_limited: '0',
            pending: largeCount,
            disabled: '0',
            suspended: '0',
            exhausted: '0',
            expired: '0',
            ended: '0',
          },
        }),
      ]),
      {},
      '1',
      20,
    );
    expect(result.data[0]?.keyCount).toBe(largeCount);
    expect(
      normalizeOwnerDonationsPage(
        page([summary({ state_counts: { ...summary().state_counts, ended: '1' } })]),
        {},
        '1',
        20,
      ).data[0]?.stateCounts.ended,
    ).toBe('1');
  });

  it('shows a forced rejection as rejected with its reason', () => {
    const result = normalizeOwnerDonationsPage(
      page([
        summary({
          status: 'rejected',
          review_result: {
            decision: 'force_reject',
            reason: 'Review required',
            reviewed_at: 1_800_000_001,
          },
          state_counts: { ...summary().state_counts, pending: '0', ended: '1' },
        }),
      ]),
      {},
      '1',
      20,
    ).data[0];
    expect(result.reviewResult).toMatchObject({ decision: 'reject', reason: 'Review required' });
  });

  it.each(['current', 'all'] as const)(
    'renders multiline review history in the %s view',
    (view) => {
      const review = {
        decision: 'approve',
        reason: 'First review line\nSecond review line',
        reviewed_at: 1_800_000_002,
      };
      const result = normalizeOwnerDonationsPage(
        page([
          summary({
            status: 'approved',
            review_result: review,
            state_counts: { ...summary().state_counts, pending: '0', available: '1' },
          }),
        ]),
        { view },
        '1',
        20,
      );
      expect(result.data[0]?.reviewResult).toEqual({
        decision: 'approve',
        reason: review.reason,
        reviewedAt: review.reviewed_at,
      });
      expect(result.data[0]?.status).toBe('approved');
    },
  );

  it('keeps an approval history when the parent is pending and the summary is absent', () => {
    const review = { decision: 'approve', reason: 'Earlier approval', reviewed_at: 1_799_999_999 };
    const result = normalizeOwnerDonationsPage(
      page([summary({ review_result: review, use_summary: null })]),
      {},
      '1',
      20,
    );
    expect(result.data[0]).toMatchObject({
      status: 'pending',
      reviewResult: { decision: 'approve', reason: review.reason },
    });
    expect(result.data[0]?.useSummary).toBeUndefined();
  });

  it('accepts backend logical expiry and mixed pending terminal key projections', () => {
    expect(
      normalizeOwnerDonationsPage(
        page([
          summary({
            status: 'expired',
            review_result: null,
            state_counts: { ...summary().state_counts, pending: '0', expired: '1' },
          }),
        ]),
        {},
        '1',
        20,
      ).data[0]?.reviewResult,
    ).toBeNull();

    for (const terminalState of ['expired', 'ended'] as const) {
      const result = normalizeOwnerDonationsPage(
        page([
          summary({
            key_count: '2',
            state_counts: {
              ...summary().state_counts,
              pending: '1',
              [terminalState]: '1',
            },
          }),
        ]),
        {},
        '1',
        20,
      );
      expect(result.data[0]?.status).toBe('pending');
      expect(result.data[0]?.stateCounts[terminalState]).toBe('1');
    }
  });

  it('keeps server preview metadata and ignores unused extension content', () => {
    const result = normalizeOwnerDonationsPage(
      page([
        summary({
          source_count: '4',
          sources: [customSource()],
          extension: { unused_text: '\ud800' },
        }),
      ]),
      {},
      '1',
      20,
    );
    expect(result.data[0]?.sourceCount).toBe('4');
    expect(result.data[0]?.sources).toHaveLength(1);
    expect(() =>
      normalizeOwnerDonationsPage(page([summary({ description: 7 })]), {}, '1', 20),
    ).toThrow(ApiError);
  });

  it('rejects duplicate summary identities and strips unused wire fields', () => {
    expect(() =>
      normalizeOwnerDonationsPage(
        page([summary(), summary({ updated_at: 1_800_000_002 })], {
          total_items: '2',
          total_pages: '1',
        }),
        {},
        '1',
        20,
      ),
    ).toThrow(ApiError);
    expect(
      normalizeOwnerDonationsPage(page([summary({ handling: { state: 'pending' } })]), {}, '1', 20),
    ).toEqual(normalizeOwnerDonationsPage(page([summary()]), {}, '1', 20));
    expect(normalizeOwnerDonationsPage({ ...page([summary()]), extra: true }, {}, '1', 20)).toEqual(
      normalizeOwnerDonationsPage(page([summary()]), {}, '1', 20),
    );
  });
});

describe('owner donation key numbered-page normalizers', () => {
  it('projects the owner key, recurring receipt, and bounded rule preview', () => {
    const result = normalizeOwnerDonationKeysPage(
      page([
        key({
          rule_count: '4',
          rules: [rule(RULE_IDS[0]!, { state: 'limited' }), rule(RULE_IDS[1]!), rule(RULE_IDS[2]!)],
        }),
      ]),
      OWNER_ID,
      '1',
      20,
    );
    expect(result.data[0]).toMatchObject({
      id: KEY_ID,
      donationId: OWNER_ID,
      keyId: KEY_ID,
      donationRevision: '1',
      ruleCount: '4',
    });
    expect(result.data[0]?.rules).toHaveLength(3);
    expect(result.data[0]?.rules[0]).toMatchObject({ id: RULE_IDS[0], state: 'limited' });
    expect(result.data[0]?.usage.priceUsed).toBe('0');
  });

  it('preserves long amounts, rejects key parent/id mismatches and strips private fields', () => {
    const result = normalizeOwnerDonationKeysPage(
      page([
        key({
          usage: {
            ...key().usage,
            price_used: U128_MAX_AMOUNT,
          },
        }),
      ]),
      OWNER_ID,
      '1',
      20,
    );
    expect(result.data[0]?.usage.priceUsed).toBe(U128_MAX_AMOUNT);
    expect(() =>
      normalizeOwnerDonationKeysPage(page([key({ donation_id: '42' })]), OWNER_ID, '1', 20),
    ).toThrow(ApiError);
    expect(() =>
      normalizeOwnerDonationKeysPage(page([key({ key_id: '52' })]), OWNER_ID, '1', 20),
    ).toThrow(ApiError);
    expect(
      normalizeOwnerDonationKeysPage(page([key({ safe_note: 'private' })]), OWNER_ID, '1', 20),
    ).toEqual(normalizeOwnerDonationKeysPage(page([key()]), OWNER_ID, '1', 20));
  });

  it('renders the supplied rule preview independently of its count and ordering', () => {
    const result = normalizeOwnerDonationKeysPage(
      page([
        key({
          rule_count: '4',
          rules: [rule(RULE_IDS[0]!), rule(RULE_IDS[1]!, { state: 'limited' })],
        }),
      ]),
      OWNER_ID,
      '1',
      20,
    );
    expect(result.data[0]?.ruleCount).toBe('4');
    expect(result.data[0]?.rules.map((entry) => entry.state)).toEqual(['available', 'limited']);
    expect(() =>
      normalizeOwnerDonationKeysPage(
        page([
          key({
            rule_count: '2',
            rules: [rule(RULE_IDS[0]!), rule(RULE_IDS[0]!)],
          }),
        ]),
        OWNER_ID,
        '1',
        20,
      ),
    ).toThrow(ApiError);
  });

  it('rejects duplicate key rows and non-null cursors', () => {
    expect(() =>
      normalizeOwnerDonationKeysPage(
        page([key(), key({ donation_revision: '2' })], { total_items: '2', total_pages: '1' }),
        OWNER_ID,
        '1',
        20,
      ),
    ).toThrow(ApiError);
    expect(() =>
      normalizeOwnerDonationKeysPage(
        { ...page([key()]), next_cursor: 'cursor' },
        OWNER_ID,
        '1',
        20,
      ),
    ).toThrow(ApiError);
  });
});

describe('owner donation numbered-page requests', () => {
  it('uses only the owner status/q filter contract and passes the abort signal', async () => {
    let requestSignal: AbortSignal | null = null;
    const fetchMock = vi.fn<typeof fetch>(async (input, init) => {
      requestSignal = init?.signal ?? null;
      expect(String(input)).toBe(
        '/api/donations?status=approved&q=%E6%8F%8F%E8%BF%B0&page=1&page_size=10',
      );
      return jsonResponse(
        page(
          [
            summary({
              status: 'approved',
              review_result: { decision: 'approve', reason: '', reviewed_at: 1_800_000_001 },
              state_counts: { ...summary().state_counts, pending: '0', available: '1' },
            }),
          ],
          { page_size: 10 },
        ),
      );
    });
    vi.stubGlobal('fetch', fetchMock);
    const controller = new AbortController();

    const result = await getOwnerDonationsPage(
      { status: 'approved', q: '描述' },
      '1',
      10,
      controller.signal,
    );
    expect(result.data[0]?.status).toBe('approved');
    expect(requestSignal).toBe(controller.signal);
  });

  it('uses the owner key path and rejects malformed inputs before network access', async () => {
    const fetchMock = vi.fn<typeof fetch>(async () =>
      jsonResponse(page([key()], { page_size: 10 })),
    );
    vi.stubGlobal('fetch', fetchMock);
    await getOwnerDonationKeysPage(OWNER_ID, '1', 10);
    expect(String(fetchMock.mock.calls[0]?.[0])).toBe('/api/donations/41/keys?page=1&page_size=10');

    await expect(
      getOwnerDonationsPage({ handling: 'pending' } as never, '1', 20),
    ).rejects.toMatchObject({ code: 'invalid_request', status: 400 });
    await expect(
      getOwnerDonationsPage({ status: 'unknown' } as never, '1', 20),
    ).rejects.toMatchObject({
      code: 'invalid_request',
      status: 400,
    });
    await expect(getOwnerDonationsPage({ q: '\u0000' }, '1', 20)).rejects.toMatchObject({
      status: 400,
    });
    await expect(getOwnerDonationKeysPage('0', '1', 20)).rejects.toMatchObject({ status: 400 });
    await expect(getOwnerDonationKeysPage(OWNER_ID, '1', 30 as never)).rejects.toMatchObject({
      status: 400,
    });
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it('accepts the exact search limits and rejects overlong or ill-formed search input', async () => {
    const fetchMock = vi.fn<typeof fetch>(async () => jsonResponse(page([summary()])));
    vi.stubGlobal('fetch', fetchMock);
    await getOwnerDonationsPage({ q: '😀'.repeat(128) }, '1', 20);
    await expect(getOwnerDonationsPage({ q: '😀'.repeat(129) }, '1', 20)).rejects.toMatchObject({
      code: 'invalid_request',
      status: 400,
    });
    await expect(getOwnerDonationsPage({ q: '\ud800' }, '1', 20)).rejects.toMatchObject({
      code: 'invalid_request',
      status: 400,
    });
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it('preserves an unauthenticated response and marks a malformed 200 as invalid_response', async () => {
    const unauthorized = vi.fn<typeof fetch>(async () =>
      jsonResponse({ error: { code: 'unauthorized', message: 'login required' } }, 401),
    );
    vi.stubGlobal('fetch', unauthorized);
    await expect(getOwnerDonationsPage({}, '1', 20)).rejects.toMatchObject({
      code: 'unauthorized',
      status: 401,
    });

    const forbidden = vi.fn<typeof fetch>(async () =>
      jsonResponse({ error: { code: 'forbidden', message: 'not allowed' } }, 403),
    );
    vi.stubGlobal('fetch', forbidden);
    await expect(getOwnerDonationKeysPage(OWNER_ID, '1', 20)).rejects.toMatchObject({
      code: 'forbidden',
      status: 403,
    });

    const malformed = vi.fn<typeof fetch>(async () => jsonResponse({ data: [] }));
    vi.stubGlobal('fetch', malformed);
    await expect(getOwnerDonationsPage({}, '1', 20)).rejects.toMatchObject({
      code: 'invalid_response',
      status: 200,
    });
  });
});

it('accepts a rejected use summary on a live pending donation and sends the separate display filter', async () => {
  const payload = page([
    summary({
      use_summary: {
        total: '2',
        approved: '0',
        pending: '0',
        rejected: '2',
        withdrawn: '0',
        review_status: 'rejected',
      },
    }),
  ]);
  const fetchMock = vi
    .fn<typeof fetch>()
    .mockResolvedValue(
      new Response(JSON.stringify(payload), { headers: { 'content-type': 'application/json' } }),
    );
  vi.stubGlobal('fetch', fetchMock);
  const result = await getOwnerDonationsPage({ review_status: 'rejected' }, '1', 20);
  expect(String(fetchMock.mock.calls[0]![0])).toContain('review_status=rejected');
  expect(result.data[0]!.status).toBe('pending');
  expect(result.data[0]!.useSummary?.review_status).toBe('rejected');
});
