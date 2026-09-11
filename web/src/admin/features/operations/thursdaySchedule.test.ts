import { describe, expect, it } from 'vitest';
import { formatBeijingTime, nextThursdaySchedule } from './thursdaySchedule';

describe('Thursday scheduling in Beijing time', () => {
  it.each([
    ['2026-09-09T15:59:59.999Z', '2026-09-10'],
    ['2026-09-09T16:00:00.000Z', '2026-09-17'],
    ['2026-09-10T12:00:00.000Z', '2026-09-17'],
    ['2026-09-11T01:00:00.000Z', '2026-09-17'],
    ['2026-09-30T15:59:59.999Z', '2026-10-01'],
    ['2026-12-30T15:59:59.999Z', '2026-12-31'],
    ['2026-12-30T16:00:00.000Z', '2027-01-07'],
    ['2028-02-28T16:00:00.000Z', '2028-03-02'],
  ])('at %s selects %s without using the browser time zone', (now, date) => {
    const schedule = nextThursdaySchedule(Date.parse(now));
    expect(schedule).toEqual({
      period_key: date,
      opens_at: Date.parse(`${date}T00:00:00+08:00`) / 1_000,
      closes_at: Date.parse(`${date}T00:00:00+08:00`) / 1_000 + 86_400,
    });
    expect(formatBeijingTime(schedule.opens_at)).toBe(`${date} 00:00`);
  });
});
