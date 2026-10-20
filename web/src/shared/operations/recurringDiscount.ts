import {
  array,
  boolean,
  exactRecord,
  integer,
  invalidResponse,
  nullableUnixSecond,
  oneOf,
  string,
} from './wire';

export interface DiscountPeriod {
  weekdays: number[];
  start_time: string;
  end_time: string;
  percent: number;
}
export interface RecurringDiscount {
  enabled: boolean;
  time_zone: string;
  default_percent: number;
  periods: DiscountPeriod[];
}
export interface EffectiveDiscount {
  source: 'none' | 'fixed' | 'recurring';
  percent: number;
  next_change_at: number | null;
}
export const defaultRecurringDiscount = (): RecurringDiscount => ({
  enabled: false,
  time_zone: 'UTC',
  default_percent: 100,
  periods: [],
});
export function validPeriodTime(value: string, end = false): boolean {
  return /^(?:[01][0-9]|2[0-3]):[0-5][0-9]$/.test(value) || (end && value === '24:00');
}
export function normalizeRecurringDiscount(value: unknown): RecurringDiscount {
  const item = exactRecord(
    value,
    ['enabled', 'time_zone', 'default_percent', 'periods'],
    'recurring discount',
  );
  const timeZone = string(item.time_zone, 'discount time zone', { min: 1, ascii: true });
  if (timeZone.length > 64) invalidResponse('discount time zone');
  return {
    enabled: boolean(item.enabled, 'recurring discount enabled'),
    time_zone: timeZone,
    default_percent: integer(item.default_percent, 'default discount percent', 0, 100),
    periods: array(item.periods, 'discount periods', 16).map((value) => {
      const period = exactRecord(
        value,
        ['weekdays', 'start_time', 'end_time', 'percent'],
        'discount period',
      );
      const weekdays = array(period.weekdays, 'discount weekdays', 7).map((day) =>
        integer(day, 'discount weekday', 1, 7),
      );
      const start = string(period.start_time, 'discount start time', { ascii: true });
      const end = string(period.end_time, 'discount end time', { ascii: true });
      if (
        !weekdays.length ||
        weekdays.some((day, index) => index > 0 && day <= weekdays[index - 1]!) ||
        !validPeriodTime(start) ||
        !validPeriodTime(end, true) ||
        start === end
      )
        invalidResponse('discount period');
      return {
        weekdays,
        start_time: start,
        end_time: end,
        percent: integer(period.percent, 'period discount percent', 0, 100),
      };
    }),
  };
}
export function normalizeEffectiveDiscount(value: unknown): EffectiveDiscount {
  const item = exactRecord(value, ['source', 'percent', 'next_change_at'], 'effective discount');
  return {
    source: oneOf(item.source, ['none', 'fixed', 'recurring'] as const, 'discount source'),
    percent: integer(item.percent, 'effective discount percent', 0, 100),
    next_change_at: nullableUnixSecond(item.next_change_at, 'next price change'),
  };
}
export function validRecurringDiscount(value: RecurringDiscount): boolean {
  try {
    normalizeRecurringDiscount(value);
    return true;
  } catch {
    return false;
  }
}

export const weekdayKeys = [
  'common.recurringDiscount.monday',
  'common.recurringDiscount.tuesday',
  'common.recurringDiscount.wednesday',
  'common.recurringDiscount.thursday',
  'common.recurringDiscount.friday',
  'common.recurringDiscount.saturday',
  'common.recurringDiscount.sunday',
] as const;
