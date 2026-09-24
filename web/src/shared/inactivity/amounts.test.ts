import { expect, it } from 'vitest';
import { decimalAmount, scaledAmount } from './amounts';
it('round trips credit amounts beyond JavaScript integer precision', () => {
  const value = '170141183460469231731687303715884105727';
  expect(scaledAmount(decimalAmount(value, 3), 3)).toBe(value);
  expect(scaledAmount('0.001', 3)).toBe('1');
  expect(scaledAmount('1.25', 2)).toBe('125');
  expect(decimalAmount('10000', 2)).toBe('100');
});
it('rejects loss of precision, signed input, exponent notation and overflow', () => {
  for (const value of [
    '0.0001',
    '-1',
    '1e3',
    'NaN',
    '01',
    '170141183460469231731687303715884105.728',
  ])
    expect(scaledAmount(value, 3)).toBeNull();
  expect(scaledAmount('1.001', 2)).toBeNull();
});
