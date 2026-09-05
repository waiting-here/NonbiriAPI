import { describe, expect, it } from 'vitest';
import { HISTORY_KINDS, normalizeHistory } from './data';

const entry = {
  operation_id: `op_${'A'.repeat(22)}`,
  line: 1,
  kind: 'charity_settle',
  delta: '0.007',
  created_at: 1_800_000_000,
  request_id: `req_${'B'.repeat(21)}A`,
};
const page = {
  data: [entry],
  page: '1',
  page_size: 20,
  total: '1',
  total_pages: '1',
  anchor: entry.operation_id,
  current_balance: '9000000000000.007',
  server_now: 1_800_000_001,
};

describe('owner credit history projection', () => {
  it('preserves wide signed decimal values without coercion', () => {
    expect(normalizeHistory(page).current_balance).toBe('9000000000000.007');
    expect(normalizeHistory({ ...page, data: [{ ...entry, delta: '-0.007' }] }).data[0].delta).toBe(
      '-0.007',
    );
  });

  it.each(HISTORY_KINDS)('accepts the %s reason without leaking a source', (kind) => {
    expect(
      normalizeHistory({ ...page, data: [{ ...entry, kind, request_id: null }] }).data[0].kind,
    ).toBe(kind);
  });

  it('rejects a request attached to donor rewards or unrelated entries', () => {
    for (const kind of ['donor_reward', 'admin_user_adjustment', 'fishing_settle']) {
      expect(() => normalizeHistory({ ...page, data: [{ ...entry, kind }] })).toThrow(
        /association/i,
      );
    }
  });

  it('accepts an owner-scoped request link for a short-request penalty', () => {
    expect(
      normalizeHistory({
        ...page,
        data: [{ ...entry, kind: 'anti_abuse_penalty', delta: '-0.007' }],
      }).data[0].request_id,
    ).toBe(entry.request_id);
  });

  it('rejects unsafe extra fields, duplicate rows and inconsistent pagination', () => {
    for (const invalid of [
      { ...page, data: [{ ...entry, source_id: 'private-claim' }] },
      { ...page, data: [{ ...entry, reason: 'private-admin-note' }] },
      { ...page, data: [{ ...entry, delta: '0' }] },
      { ...page, data: [{ ...entry, delta: 0.007 }] },
      { ...page, data: [{ ...entry, request_id: 'req_wrong' }] },
      { ...page, data: [entry, entry], total: '2' },
      { ...page, page: '2' },
      { ...page, total_pages: '3' },
      { ...page, page_size: 21 },
      { ...page, total: '20' },
      { ...page, anchor: null },
    ])
      expect(() => normalizeHistory(invalid)).toThrow();
  });

  it('accepts an empty filter result with or without a browsing anchor', () => {
    for (const anchor of [null, entry.operation_id])
      expect(normalizeHistory({ ...page, data: [], total: '0', anchor }).data).toEqual([]);
  });
});
