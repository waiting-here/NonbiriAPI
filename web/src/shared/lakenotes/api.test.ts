import { describe, expect, it } from 'vitest';
import { naturalToUnits, unitsToNatural } from './api';

describe('Lake API amounts and directory', () => {
  it('keeps whole coins and three decimal credits exact beyond Number precision', () => {
    expect(naturalToUnits('9007199254740993', 'coins')).toBe('9007199254740993');
    expect(naturalToUnits('9007199254740993.001', 'general')).toBe('9007199254740993001');
    expect(unitsToNatural('9007199254740993001', 'game')).toBe('9007199254740993.001');
    expect(unitsToNatural('-1', 'general')).toBe('-0.001');
    expect(unitsToNatural('-1200', 'general')).toBe('-1.2');
    for (const value of ['1.0001', '1e3', ' 2', '01', '-1'])
      expect(() => naturalToUnits(value, 'general')).toThrow();
    expect(() => naturalToUnits('1.1', 'coins')).toThrow();
    expect(() => naturalToUnits('0', 'coins')).toThrow();
    expect(naturalToUnits('0', 'general', true)).toBe('0');
  });
});
