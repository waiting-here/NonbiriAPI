import { describe, expect, it } from 'vitest';
import { roleLogKeys, normalizeAdminLogAttempt, normalizeAdminLogRow } from './data';

const usage = {
  uncached_input_tokens: '0',
  cache_write_input_tokens: '0',
  cache_read_input_tokens: '0',
  output_tokens: '0',
  total_tokens: '0',
  usage_unknown: false,
  charge: '0',
};

const row = {
  id: `req_${'A'.repeat(22)}`,
  route_kind: 'openai_chat_completions',
  caller_result_class: 'success',
  caller_status: 200,
  caller_error_code: null,
  started_at: 1,
  completed_at: 2,
  usage,
  user_id: '1',
  attempt_count: '1',
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

  it('enforces the logical caller terminal matrix', () => {
    expect(normalizeAdminLogRow(row).caller_result_class).toBe('success');
    expect(() => normalizeAdminLogRow({ ...row, caller_status: 500 })).toThrow(/successful/i);
    expect(() => normalizeAdminLogRow({ ...row, caller_result_class: null })).toThrow(
      /nonterminal/i,
    );
    expect(() =>
      normalizeAdminLogRow({
        ...row,
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
});
