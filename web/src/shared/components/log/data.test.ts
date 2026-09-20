import { describe, expect, it } from 'vitest';
import {
  normalizeAdminLogAttempt,
  normalizeAdminLogRow,
  normalizeStewardLogDetail,
  normalizeStewardLogRow,
  normalizeUserLogDetail,
  normalizeUserLogRow,
  roleLogExportPath,
  roleLogKeys,
} from './data';

const syntheticDiscordID = '1'.repeat(18);

const usage = {
  uncached_input_tokens: '0',
  cache_write_input_tokens: '0',
  cache_read_input_tokens: '0',
  output_tokens: '0',
  total_tokens: '0',
  usage_unknown: false,
  charge: '0',
};

const commonRow = {
  id: `req_${'A'.repeat(22)}`,
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
};

const row = {
  ...commonRow,
  user_id: '1',
  caller_identity: null,
  attempt_count: '1',
};

const userRow = {
  ...commonRow,
  model: 'model',
  attempt_count: '1',
};

const stewardRow = {
  ...commonRow,
  user_id: '1',
  caller_identity: null,
  attempt_count: '1',
};

const charityStewardRow = {
  ...stewardRow,
  route_kind: 'charity_chat_completions',
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

describe('role log wire', () => {
  it('rejects inconsistent pre-handler accounting and validates every role', () => {
    const refusal = {
      ...commonRow,
      phase: 'pre_handler',
      rejection_stage: 'preflight',
      rejection_reason: 'invalid_request',
      request_method: 'POST',
      request_path: '/v1/chat/completions',
      caller_result_class: 'failed',
      caller_status: 400,
      caller_error_code: 'invalid_request',
      attempt_count: '0',
    };
    expect(normalizeUserLogRow({ ...refusal, model: '' }).phase).toBe('pre_handler');
    for (const normalize of [normalizeAdminLogRow, normalizeStewardLogRow]) {
      const managed = { ...refusal, user_id: '1', caller_identity: null };
      expect(normalize(managed).phase).toBe('pre_handler');
      for (const patch of [
        { attempt_count: '1' },
        { usage: { ...usage, charge: '1' } },
        { rejection_reason: 'raw_secret' },
        { request_path: '/unknown' },
        { request_method: 'GET' },
        { phase: 'handler' },
      ])
        expect(() => normalize({ ...managed, ...patch })).toThrow();
    }
    expect(roleLogExportPath('user', { phase: 'pre_handler', model: 'm' }, 'json')).toBe(
      '/api/logs/export.json?phase=pre_handler&model=m',
    );
  });
  it.each(['openai_embeddings', 'charity_embeddings'] as const)(
    'keeps the ownership and privacy projection for %s',
    (route) => {
      const charity = route === 'charity_embeddings';
      const request = {
        ...commonRow,
        route_kind: route,
        model: 'provider/model',
        ...(charity ? {} : { attempt_count: '1' }),
        usage: { ...usage, usage_unknown: true },
      };
      expect(normalizeUserLogRow(request)).toMatchObject({
        route_kind: route,
        kind: charity ? 'charity' : 'self',
        usage: { usage_unknown: true },
      });
      const detail = charity
        ? { request, caller_safe_result: { class: 'success' } }
        : { request, attempts: { data: [], next_cursor: null } };
      expect(normalizeUserLogDetail(detail).request.route_kind).toBe(route);
      if (charity) {
        expect(() => normalizeUserLogRow({ ...request, attempt_count: '1' })).toThrow(/charity/);
        expect(() =>
          normalizeUserLogDetail({ ...detail, attempts: { data: [], next_cursor: null } }),
        ).toThrow();
      }
      for (const normalize of [normalizeAdminLogRow, normalizeStewardLogRow]) {
        const management = { ...row, route_kind: route };
        expect(normalize(management).route_kind).toBe(route);
        const identity = { discord_nickname: 'Current caller', discord_id: syntheticDiscordID };
        if (charity)
          expect(normalize({ ...management, caller_identity: identity }).caller_identity).toEqual(
            identity,
          );
        else
          expect(() => normalize({ ...management, caller_identity: identity })).toThrow(
            /caller identity/,
          );
      }
      expect(() => normalizeUserLogRow({ ...request, route_kind: 'future_embeddings' })).toThrow(
        /route kind/,
      );
    },
  );

  it('keeps every role under its station-owned cache root', () => {
    expect(roleLogKeys.root('admin')).toEqual(['admin', 'operations', 'logs']);
    expect(roleLogKeys.root('user')).toEqual(['user', 'operations', 'logs']);
    expect(roleLogKeys.root('steward')).toEqual(['user', 'operations', 'steward', 'logs']);
    expect(roleLogKeys.detail('admin', row.id, null, 50)).toEqual([
      'admin',
      'operations',
      'logs',
      'detail',
      row.id,
      null,
      50,
    ]);
  });

  it('builds management exports with the active role and filters', () => {
    expect(
      roleLogExportPath('admin', { user_id: '7', status: '500', from: 10, to: 20 }, 'csv'),
    ).toBe('/admin/api/logs/export.csv?user_id=7&status=500&from=10&to=20');
    expect(
      roleLogExportPath('steward', { user_id: '7', status: '500', from: 10, to: 20 }, 'json'),
    ).toBe('/api/steward/logs/export.json?user_id=7&status=500&from=10&to=20');
  });

  it('enforces the logical caller terminal matrix', () => {
    expect(normalizeAdminLogRow(row).caller_result_class).toBe('success');
    expect(() => normalizeAdminLogRow({ ...row, caller_status: 500 })).toThrow(/successful/i);
    expect(() => normalizeAdminLogRow({ ...row, caller_result_class: null })).toThrow(
      /nonterminal/i,
    );
    expect(() =>
      normalizeAdminLogRow({
        ...row,
        phase: 'handler' as const,
        rejection_stage: null,
        rejection_reason: null,
        request_method: null,
        request_path: null,
        caller_result_class: 'cancelled',
        caller_status: null,
        completed_at: null,
      }),
    ).toThrow(/cancelled/i);
  });

  it('accepts synthetic attempts without a status and discovery empty model ids', () => {
    expect(
      normalizeAdminLogAttempt({
        ...attempt,
        result_kind: 'synthetic',
        status_code: null,
        upstream_model_id: '',
      }).status_code,
    ).toBeNull();
    expect(() => normalizeAdminLogAttempt({ ...attempt, status_code: null })).toThrow(/status/i);
  });

  it('rejects noncanonical endpoint snapshots and unsafe diagnostics', () => {
    expect(() =>
      normalizeAdminLogAttempt({
        ...attempt,
        endpoint_base_url: 'https://api.example.com/v1?secret=x',
      }),
    ).toThrow(/base URL/i);
    expect(() => normalizeAdminLogAttempt({ ...attempt, diag: 'first\nsecond' })).toThrow(
      /diagnostic/i,
    );
  });

  it('normalizes the steward caller identity on rows and details', () => {
    const complete = normalizeStewardLogRow({
      ...charityStewardRow,
      caller_identity: {
        discord_nickname: 'Ada Example',
        discord_id: syntheticDiscordID,
      },
    });
    expect(complete.caller_identity).toEqual({
      discord_nickname: 'Ada Example',
      discord_id: syntheticDiscordID,
    });

    const partial = normalizeStewardLogRow({
      ...charityStewardRow,
      caller_identity: { discord_nickname: null, discord_id: syntheticDiscordID },
    });
    expect(partial.caller_identity?.discord_nickname).toBeNull();
    expect(partial.caller_identity?.discord_id).toBe(syntheticDiscordID);
    expect(normalizeStewardLogRow(stewardRow).caller_identity).toBeNull();

    const detail = normalizeStewardLogDetail({
      request: {
        ...charityStewardRow,
        caller_identity: {
          discord_nickname: 'Ada Example',
          discord_id: syntheticDiscordID,
        },
      },
      attempts: { data: [], next_cursor: null },
    });
    expect(detail.request.caller_identity?.discord_id).toBe(syntheticDiscordID);
  });

  it('keeps caller identity scoped to steward rows and requires explicit null', () => {
    expect(() => normalizeStewardLogRow({ ...stewardRow, caller_identity: undefined })).toThrow(
      /caller identity/i,
    );
    expect(() =>
      normalizeStewardLogRow({
        ...charityStewardRow,
        caller_identity: { discord_nickname: 'Ada Example' },
      }),
    ).toThrow(/caller identity/i);
    expect(() =>
      normalizeStewardLogRow({
        ...stewardRow,
        caller_identity: { discord_nickname: 'Ada Example', discord_id: syntheticDiscordID },
      }),
    ).toThrow(/steward caller identity/i);
    expect(() => normalizeAdminLogRow({ ...row, caller_identity: null, secret: 'never' })).toThrow(
      /administrator log row/i,
    );
    expect(() => normalizeUserLogRow({ ...userRow, caller_identity: null })).toThrow(
      /user log row/i,
    );
  });

  it('accepts bounded long caller values and rejects values beyond the wire limit', () => {
    const nickname = 'N'.repeat(256);
    const discordID = '9'.repeat(128);
    expect(
      normalizeStewardLogRow({
        ...charityStewardRow,
        caller_identity: { discord_nickname: nickname, discord_id: discordID },
      }).caller_identity,
    ).toEqual({ discord_nickname: nickname, discord_id: discordID });
    expect(() =>
      normalizeStewardLogRow({
        ...charityStewardRow,
        caller_identity: { discord_nickname: `${nickname}N`, discord_id: discordID },
      }),
    ).toThrow(/caller Discord nickname/i);
    expect(() =>
      normalizeStewardLogRow({
        ...charityStewardRow,
        caller_identity: { discord_nickname: 'Ada', discord_id: '9'.repeat(129) },
      }),
    ).toThrow(/caller Discord ID/i);
    expect(() =>
      normalizeStewardLogRow({
        ...charityStewardRow,
        caller_identity: { discord_nickname: '名'.repeat(86), discord_id: discordID },
      }),
    ).toThrow(/caller Discord nickname/i);
    expect(() =>
      normalizeStewardLogRow({
        ...charityStewardRow,
        caller_identity: { discord_nickname: 'Ada', discord_id: '界'.repeat(43) },
      }),
    ).toThrow(/caller Discord ID/i);
    const unicodeID = '界'.repeat(42);
    expect(
      normalizeStewardLogRow({
        ...charityStewardRow,
        caller_identity: { discord_nickname: 'Ada', discord_id: unicodeID },
      }).caller_identity?.discord_id,
    ).toBe(unicodeID);
  });
});
