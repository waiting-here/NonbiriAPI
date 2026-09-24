import { describe, expect, it } from 'vitest';
import { limitedActivity, activityExchange } from '../../../test/fixtures/limitedActivities';
import {
  decodeDetail,
  decodeExchange,
  decodeSupply,
  exchangeCost,
  normalizePrice,
  parseUTC,
  utcInput,
} from './api';
describe('limited activity exact values and projections', () => {
  it('keeps cap counters beyond Number precision and checks remaining capacity', () => {
    const detail = limitedActivity({
      module_config: {
        paper_price: '0.001',
        brush_price: '10000',
        brush_cap: '9007199254740993123',
        brush_exchanged: '9007199254740993122',
        brush_remaining: '1',
      },
    });
    expect(decodeDetail(detail).module_config.brush_cap).toBe('9007199254740993123');
    expect(() => decodeSupply({ ...detail.module_config, brush_remaining: '2' })).toThrow();
  });
  it('rejects private fields and inconsistent receipt charges', () => {
    expect(() => decodeDetail({ ...limitedActivity(), upstream: 'private' })).toThrow();
    const value = activityExchange();
    expect(decodeExchange(value).receipt.cost).toBe('1000');
    expect(() => decodeExchange({ ...value, receipt: { ...value.receipt, cost: '1' } })).toThrow();
    expect(() =>
      decodeExchange({ ...value, receipt: { ...value.receipt, user_id: '2' } }),
    ).toThrow();
  });
  it('quotes with exact arithmetic and rejects overflow or fractional units', () => {
    expect(exchangeCost('0.001', '9007199254740993')).toBe('9007199254740.993');
    expect(exchangeCost('10000', '2')).toBe('20000');
    expect(exchangeCost('1000', '1.5')).toBeNull();
    expect(exchangeCost('1000', '9'.repeat(39))).toBeNull();
    expect(normalizePrice('1000.100')).toBe('1000.1');
    expect(() => normalizePrice('0')).toThrow();
    expect(() => normalizePrice('0.0001')).toThrow();
  });
  it('uses explicit UTC and rejects normalized invalid calendar dates', () => {
    const text = '2026-03-08T02:30:00';
    expect(utcInput(parseUTC(text))).toBe(text);
    expect(parseUTC('')).toBeNull();
    expect(() => parseUTC('2026-02-30T10:00:00')).toThrow();
    expect(() => decodeDetail(limitedActivity({ starts_at: 10, ends_at: 10 }))).toThrow();
  });
});
