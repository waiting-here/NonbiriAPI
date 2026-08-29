import { describe, expect, test } from 'vitest';
import { formatAmount } from './Amount';

describe('Amount', () => {
  test('converts canonical milli-credits without floating point rounding', () => {
    expect(formatAmount('0')).toBe('0');
    expect(formatAmount('1')).toBe('0.001');
    expect(formatAmount('1000')).toBe('1');
    expect(formatAmount('1001')).toBe('1.001');
    expect(formatAmount('1250')).toBe('1.25');
    expect(formatAmount('2500000125')).toBe('2,500,000.125');
    expect(formatAmount('-1001')).toBe('-1.001');
    expect(formatAmount('-1')).toBe('-0.001');
  });

  test('keeps arbitrary-size integers exact and drops insignificant zeroes', () => {
    expect(formatAmount('9223372036854775807')).toBe('9,223,372,036,854,775.807');
    expect(formatAmount('1000000')).toBe('1,000');
    expect(formatAmount('1200')).toBe('1.2');
  });

  test('rejects non-canonical or hostile string values instead of guessing', () => {
    for (const value of [
      '',
      ' 1000',
      '1000 ',
      '+1000',
      '01',
      '-0',
      '1.0',
      '1e3',
      'Infinity',
      '0x10',
      '1000\n',
    ]) {
      expect(formatAmount(value), value).toBe('—');
    }
  });

  test('accepts trusted bigint values for internal callers', () => {
    expect(formatAmount(1001n)).toBe('1.001');
    expect(formatAmount(-1001n)).toBe('-1.001');
  });
});
