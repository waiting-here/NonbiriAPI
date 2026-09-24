import { afterEach, describe, expect, it, vi } from 'vitest';
import { installJsonFetchFixtures } from '../../../../test/unit/support';
import {
  getRoleLogDetailPage,
  getRoleLogsPage,
  normalizeRoleLogDetail,
  normalizeRoleLogPage,
  numberedLogKeys,
} from './numberedQueries';

const requestID = `req_${'A'.repeat(21)}Q`;
const usage = {
  uncached_input_tokens: '0',
  cache_write_input_tokens: '0',
  cache_read_input_tokens: '0',
  output_tokens: '1',
  total_tokens: '1',
  usage_unknown: false,
  charge: '0',
};

const row = {
  id: requestID,
  route_kind: 'openai_chat_completions',
  phase: 'handler' as const,
  rejection_stage: null,
  rejection_reason: null,
  request_method: null,
  request_path: null,
  caller_result_class: 'success',
  caller_status: 200,
  caller_error_code: null,
  started_at: 1,
  completed_at: 2,
  usage,
  model: 'model',
  attempt_count: '1',
};

const adminRow = {
  id: row.id,
  route_kind: row.route_kind,
  phase: 'handler' as const,
  rejection_stage: null,
  rejection_reason: null,
  request_method: null,
  request_path: null,
  caller_result_class: row.caller_result_class,
  caller_status: row.caller_status,
  caller_error_code: row.caller_error_code,
  started_at: row.started_at,
  completed_at: row.completed_at,
  usage: row.usage,
  user_id: '7',
  caller_identity: null,
  attempt_count: row.attempt_count,
};

const stewardRow = {
  ...adminRow,
};

const attempt = {
  attempt_seq: '1',
  result_kind: 'response',
  endpoint_key_id: '2',
  endpoint_base_url: 'https://api.example.com/v1',
  connector_type: 'openai-compatible',
  upstream_model_id: 'model',
  status_code: 200,
  upstream_code: null,
  diag: null,
  usage,
  started_at: 1,
  completed_at: 2,
};

const userAttempt = {
  ...attempt,
  endpoint_note: '',
  key_note: '',
};

function pagination(page = '1', pageSize = 20, totalItems = 1, totalPages = 1) {
  return {
    page,
    page_size: pageSize,
    total_items: String(totalItems),
    total_pages: String(totalPages),
  };
}

function list(data: unknown[] = [row], meta = pagination()) {
  return { data, next_cursor: null, pagination: meta };
}

function detail(overrides: Record<string, unknown> = {}, attemptMeta = pagination()) {
  return {
    request: row,
    attempts: { data: [userAttempt], next_cursor: null },
    attempt_pagination: attemptMeta,
    ...overrides,
  };
}

function adminDetail(overrides: Record<string, unknown> = {}, attemptMeta = pagination()) {
  return {
    request: adminRow,
    attempts: { data: [attempt], next_cursor: null },
    attempt_pagination: attemptMeta,
    ...overrides,
  };
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('numbered log wire', () => {
  it.each(['admin', 'steward'] as const)(
    'binds %s pages and cache identities to the physical key',
    async (role) => {
      const root = role === 'admin' ? '/admin/api' : '/api/steward';
      const fetchMock = installJsonFetchFixtures([
        {
          method: 'GET',
          path: `${root}/logs?endpoint_key_id=9007199254740993&page=1&page_size=20`,
          body: list([], pagination('1', 20, 0)),
        },
      ]);
      const filter = { endpoint_key_id: '9007199254740993' };
      expect(numberedLogKeys.list(role, 'viewer', '1', 20, filter)).not.toEqual(
        numberedLogKeys.list(role, 'viewer', '1', 20, { endpoint_key_id: '9007199254740994' }),
      );
      await getRoleLogsPage(role, '1', 20, filter);
      expect(fetchMock).toHaveBeenCalledTimes(1);
      for (const value of ['0', '-1', '01', 'x', '9223372036854775808']) {
        await expect(getRoleLogsPage(role, '1', 20, { endpoint_key_id: value })).rejects.toThrow();
      }
      await expect(getRoleLogsPage('user', '1', 20, filter)).rejects.toThrow();
      expect(fetchMock).toHaveBeenCalledTimes(1);
    },
  );
  it.each(['user', 'admin', 'steward'] as const)(
    'sends the selected phase for %s numbered pages',
    async (role) => {
      const root = role === 'admin' ? '/admin/api' : role === 'steward' ? '/api/steward' : '/api';
      const fetchMock = installJsonFetchFixtures([
        {
          method: 'GET',
          path: `${root}/logs?phase=pre_handler&page=1&page_size=20`,
          body: list([], pagination('1', 20, 0)),
        },
      ]);
      expect((await getRoleLogsPage(role, '1', 20, { phase: 'pre_handler' })).data).toEqual([]);
      expect(fetchMock).toHaveBeenCalledTimes(1);
      await expect(getRoleLogsPage(role, '1', 20, { phase: 'unknown' as never })).rejects.toThrow();
      expect(fetchMock).toHaveBeenCalledTimes(1);
    },
  );
  it('normalizes a page and keeps the legacy cursor field null', () => {
    expect(normalizeRoleLogPage(list(), 'user', '1', 20)).toMatchObject({
      data: [row],
      next_cursor: null,
      pagination: pagination(),
    });
  });

  it('accepts a server clamp and validates the clamped row window', () => {
    const value = list([row], pagination('2', 20, 21, 2));
    expect(normalizeRoleLogPage(value, 'user', '2147483647', 20).pagination.page).toBe('2');
  });

  it.each([
    ['an unknown envelope field', { ...list(), extra: true }],
    ['a non-null cursor', { ...list(), next_cursor: 'opaque' }],
    ['a metadata size mismatch', list([row], pagination('1', 50, 1, 1))],
    ['a row count mismatch', list([], pagination('1', 20, 1, 1))],
    ['duplicate row identities', list([row, row], pagination('1', 20, 2, 1))],
  ])('rejects %s', (_label, value) => {
    expect(() => normalizeRoleLogPage(value, 'user', '1', 20)).toThrow();
  });

  it('keeps charity details free of attempts and attempt metadata', () => {
    const charityRow = {
      id: row.id,
      route_kind: 'charity_chat_completions',
      phase: 'handler' as const,
      rejection_stage: null,
      rejection_reason: null,
      request_method: null,
      request_path: null,
      caller_result_class: row.caller_result_class,
      caller_status: row.caller_status,
      caller_error_code: row.caller_error_code,
      started_at: row.started_at,
      completed_at: row.completed_at,
      usage: row.usage,
      model: row.model,
    };
    const value = {
      request: charityRow,
      caller_safe_result: { class: 'success' },
    };
    const normalized = normalizeRoleLogDetail(value, 'user', '1', 20);
    expect(normalized).toEqual({
      kind: 'charity',
      request: expect.objectContaining({ kind: 'charity' }),
      caller_safe_result: { class: 'success' },
    });
  });

  it('normalizes attempts and their independent page metadata', () => {
    const normalized = normalizeRoleLogDetail(adminDetail(), 'admin', '1', 20);
    expect(normalized).toMatchObject({
      attempts: { data: [expect.objectContaining({ role: 'admin' })], next_cursor: null },
      attempt_pagination: pagination(),
    });
  });

  it('rejects attempt metadata that does not describe the requested window', () => {
    expect(() =>
      normalizeRoleLogDetail(adminDetail({}, pagination('1', 50, 1, 1)), 'admin', '1', 20),
    ).toThrow();
    expect(() =>
      normalizeRoleLogDetail(
        { ...adminDetail(), attempts: { data: [], next_cursor: null } },
        'admin',
        '1',
        20,
      ),
    ).toThrow();
  });
});

describe('numbered log requests', () => {
  it('rejects a detail response belonging to another request', async () => {
    installJsonFetchFixtures([
      {
        method: 'GET',
        path: `/api/logs/${requestID}?attempt_page=1&attempt_page_size=20`,
        body: detail({ request: { ...row, id: `req_${'B'.repeat(21)}Q` } }),
      },
    ]);
    await expect(getRoleLogDetailPage('user', requestID, '1', 20)).rejects.toThrow(/identity/i);
  });
  it('sends filters and only numbered list parameters', async () => {
    const fetchMock = installJsonFetchFixtures([
      {
        method: 'GET',
        path: '/admin/api/logs?user_id=7&status=500&from=10&to=20&page=2&page_size=50',
        body: list([adminRow], pagination('2', 50, 51, 2)),
      },
    ]);
    await getRoleLogsPage('admin', '2', 50, { user_id: '7', status: '500', from: 10, to: 20 });
    expect(fetchMock).toHaveBeenCalledWith(
      '/admin/api/logs?user_id=7&status=500&from=10&to=20&page=2&page_size=50',
      expect.objectContaining({ signal: undefined }),
    );
    expect(String(fetchMock.mock.calls[0]?.[0])).not.toMatch(/cursor|limit/);
  });

  it('sends the user filter for the steward management station', async () => {
    const fetchMock = installJsonFetchFixtures([
      {
        method: 'GET',
        path: '/api/steward/logs?user_id=7&page=1&page_size=20',
        body: list([stewardRow]),
      },
    ]);
    await getRoleLogsPage('steward', '1', 20, { user_id: '7' });
    expect(fetchMock).toHaveBeenCalledWith(
      '/api/steward/logs?user_id=7&page=1&page_size=20',
      expect.objectContaining({ signal: undefined }),
    );
  });

  it.each([
    ['01', 20],
    ['2147483648', 20],
    ['1', 30],
  ])('rejects invalid request parameters before network: %s/%s', async (page, size) => {
    const fetchMock = installJsonFetchFixtures([]);
    await expect(getRoleLogsPage('user', page, size as never)).rejects.toMatchObject({
      code: 'invalid_request',
      status: 400,
    });
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it.each([
    ['negative time', { from: -1 }],
    ['time after supported range', { to: 253_402_300_800 }],
    ['reversed time range', { from: 20, to: 10 }],
    ['oversized user id', { user_id: '9223372036854775808' }],
  ])('rejects invalid filters before network: %s', async (_label, filters) => {
    const fetchMock = installJsonFetchFixtures([]);
    await expect(getRoleLogsPage('admin', '1', 20, filters)).rejects.toMatchObject({
      code: 'invalid_request',
      status: 400,
    });
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('forwards AbortSignal and uses numbered attempt parameters', async () => {
    const controller = new AbortController();
    const fetchMock = installJsonFetchFixtures([
      {
        method: 'GET',
        path: `/api/logs/${requestID}?attempt_page=2&attempt_page_size=10`,
        body: detail({}, pagination('2', 10, 11, 2)),
      },
    ]);
    await getRoleLogDetailPage('user', requestID, '2', 10, controller.signal);
    expect(fetchMock).toHaveBeenCalledWith(
      `/api/logs/${requestID}?attempt_page=2&attempt_page_size=10`,
      expect.objectContaining({ signal: controller.signal }),
    );
  });

  it('keeps the account in query keys without using it in preference names', () => {
    expect(numberedLogKeys.list('steward', '42', '3', 100, {})).toEqual([
      'user',
      'operations',
      'steward',
      'logs',
      'page',
      '42',
      '3',
      100,
      {},
    ]);
    expect(JSON.stringify(numberedLogKeys.detail('user', '42', requestID, '2', 50))).toContain(
      requestID,
    );
  });
});
