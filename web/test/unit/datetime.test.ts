import { afterEach, expect, it, vi } from 'vitest';
import { formatDateTime } from '../../src/shared/utils/datetime';

afterEach(() => vi.restoreAllMocks());

it('shows Unix zero and rejects wall-clock strings without an explicit offset', () => {
  expect(formatDateTime(0)).not.toBe('—');
  expect(formatDateTime('1970-01-01T00:00:00Z')).toBe(formatDateTime(0));
  for (const value of [null, undefined, -1, NaN, '2026-01-01', '2026-01-01T12:00:00', '<script>']) {
    expect(formatDateTime(value)).toBe('—');
  }
});

it('does not retain the previous browser zone in a locale cache', () => {
  let zone = 'America/New_York';
  const native = Intl.DateTimeFormat.prototype.resolvedOptions;
  vi.spyOn(Intl.DateTimeFormat.prototype, 'resolvedOptions').mockImplementation(function (
    this: Intl.DateTimeFormat,
  ) {
    return { ...native.call(this), timeZone: zone };
  });
  const instant = Date.parse('2026-06-15T12:00:00Z') / 1000;
  const first = formatDateTime(instant, 'en');
  zone = 'Asia/Kolkata';
  const second = formatDateTime(instant, 'en');
  expect(second).not.toBe(first);
  expect(second).toMatch(/05:30|5:30|17:30/);
  zone = 'America/New_York';
  expect(formatDateTime(instant, 'en')).toBe(first);
});

it('labels the UTC fallback when Intl is unavailable', () => {
  vi.spyOn(Intl, 'DateTimeFormat').mockImplementation(() => {
    throw new Error('unavailable');
  });
  expect(formatDateTime(0)).toBe('1970-01-01 00:00 UTC');
});
