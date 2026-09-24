import { describe, expect, it } from 'vitest';
import {
  decodeOperations,
  decodeSummary,
  displayAmount,
  metrics,
  parseSiteDateTime,
  siteDateTime,
} from './api';

const zero = {
  issued: '0',
  reclaimed: '0',
  user_income: '0',
  user_expense: '0',
  internal_transfer: '0',
  operations: '0',
};
const meta = {
  asset: 'general',
  from: 1,
  to: 2,
  unit: 'milliunits',
  scale: '1000',
  offset_minutes: 330,
  ledger_seq: '0',
  projected_seq: '0',
  snapshot_at: 2,
  coverage: {
    status: 'complete',
    first_ledger_seq: null,
    first_occurred_at: null,
    unclassified_operations: '0',
    opening_known: true,
  },
};
const summary = {
  metadata: meta,
  flows: zero,
  inventory: null,
  reconciliation: {
    status: 'catching_up',
    scope: 'retained_ledger',
    inventory_net: null,
    ledger_net: null,
    interval_opening_net: null,
    interval_closing_net: null,
    interval_net_change: null,
  },
};

describe('economy audit exact monetary values', () => {
  it('keeps aggregate turnover beyond the single-entry magnitude as decimal strings', () => {
    const value = '340282366920938463463374607431768211454';
    expect(metrics({ ...zero, issued: value }).issued).toBe(value);
    expect(displayAmount(value)).toBe('340,282,366,920,938,463,463,374,607,431,768,211.454');
    expect(displayAmount('-9007199254740993123')).toBe('-9,007,199,254,740,993.123');
    expect(displayAmount('4000')).toBe('4');
  });

  it.each([1, '01', '1e3', '-0', '-1', '9'.repeat(65)])(
    'rejects noncanonical unsigned aggregate %s',
    (value) => {
      expect(() => metrics({ ...zero, issued: value })).toThrow();
    },
  );

  it('requires the fixed asset set, offset precision and distinct inventory coverage', () => {
    const ready = decodeSummary(summary);
    expect(ready.inventory).toBeNull();
    expect(ready.reconciliation.inventory_net).toBeNull();
    expect(() => decodeSummary({ ...summary, metadata: { ...meta, asset: 'cash' } })).toThrow();
    expect(() =>
      decodeSummary({ ...summary, metadata: { ...meta, offset_minutes: 15 } }),
    ).toThrow();
    expect(() => decodeSummary({ ...summary, metadata: { ...meta, scale: '1' } })).toThrow();
    expect(() => decodeSummary({ ...summary, source_prompt: 'private' })).toThrow();
  });

  it('separates the original pagination anchor from the current ledger watermark', () => {
    const page = decodeOperations({
      metadata: { ...meta, ledger_seq: '200' },
      anchor_seq: '150',
      data: [],
      next_cursor: null,
    });
    expect(page.anchor_seq).toBe('150');
    expect(page.metadata.ledger_seq).toBe('200');
  });
});

describe('fixed business time zone', () => {
  it('round trips a half-hour offset without browser time-zone conversion', () => {
    const stamp = 1_800_000_000;
    expect(parseSiteDateTime(siteDateTime(stamp, 330), 330)).toBe(stamp);
    expect(parseSiteDateTime('2027-01-15T05:30', 330)).toBe(
      Date.parse('2027-01-15T00:00:00Z') / 1000,
    );
    expect(parseSiteDateTime('2027-01-15T00:00:00', -720)).toBe(
      Date.parse('2027-01-15T12:00:00Z') / 1000,
    );
  });

  it.each([
    '2027-02-29T00:00:00',
    '2027-01-15T24:30:00',
    '2027-01-15T00:00:00Z',
    '',
    '1969-12-31T23:59:59',
  ])('rejects invalid or out-of-range time %s', (value) => {
    expect(parseSiteDateTime(value, 0)).toBeNull();
  });
});
