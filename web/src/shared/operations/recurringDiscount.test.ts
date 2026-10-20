import { describe, expect, it } from 'vitest';
import {
  defaultRecurringDiscount,
  normalizeEffectiveDiscount,
  normalizeRecurringDiscount,
} from './recurringDiscount';

describe('recurring discount DTO', () => {
  it('preserves disabled, free, empty and ordered overnight/full-day rules', () => {
    expect(
      normalizeRecurringDiscount({ ...defaultRecurringDiscount(), default_percent: 0 }),
    ).toEqual({ enabled: false, time_zone: 'UTC', default_percent: 0, periods: [] });
    const config = {
      enabled: true,
      time_zone: 'America/New_York',
      default_percent: 100,
      periods: [
        { weekdays: [7], start_time: '22:00', end_time: '02:00', percent: 50 },
        { weekdays: [2, 4], start_time: '00:00', end_time: '24:00', percent: 0 },
      ],
    };
    expect(normalizeRecurringDiscount(config)).toEqual(config);
  });
  it.each([
    null,
    {},
    { ...defaultRecurringDiscount(), enabled: undefined },
    { ...defaultRecurringDiscount(), unknown: true },
    {
      ...defaultRecurringDiscount(),
      periods: Array(17).fill({
        weekdays: [1],
        start_time: '00:00',
        end_time: '24:00',
        percent: 50,
      }),
    },
    ...[
      { weekdays: [2, 1], start_time: '00:00', end_time: '24:00', percent: 50 },
      { weekdays: [1, 1], start_time: '00:00', end_time: '24:00', percent: 50 },
      { weekdays: [], start_time: '00:00', end_time: '24:00', percent: 50 },
      { weekdays: [1], start_time: '24:00', end_time: '01:00', percent: 50 },
      { weekdays: [1], start_time: '01:00', end_time: '01:00', percent: 50 },
      { weekdays: [1], start_time: '01:00', end_time: '02:00', percent: 101 },
      { weekdays: [1], start_time: '01:00', end_time: '02:00' },
    ].map((period) => ({ ...defaultRecurringDiscount(), periods: [period] })),
  ])('rejects an incomplete or malformed full configuration', (value) => {
    expect(() => normalizeRecurringDiscount(value)).toThrow();
  });
  it('reads authoritative effective state without inferring it from device time', () => {
    expect(
      normalizeEffectiveDiscount({ source: 'recurring', percent: 0, next_change_at: 1 }),
    ).toEqual({ source: 'recurring', percent: 0, next_change_at: 1 });
    expect(() => normalizeEffectiveDiscount({ source: 'recurring', percent: 50 })).toThrow();
    expect(() =>
      normalizeEffectiveDiscount({ source: 'weekly', percent: 50, next_change_at: null }),
    ).toThrow();
  });
});
