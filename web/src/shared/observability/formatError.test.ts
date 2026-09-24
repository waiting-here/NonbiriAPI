import { describe, expect, it } from 'vitest';
import { formatErrorJSON } from './formatError';
import { errorBytes, type ErrorBody } from './api';

describe('private error rendering', () => {
  it('preserves strings, escaped quotes and exact integer tokens', () => {
    const value =
      '{"n":9007199254740993,"data":' +
      JSON.stringify({ message: '<script>"{x}"</script>', items: [] }) +
      '}';
    const formatted = formatErrorJSON(value, false);
    expect(formatted.text).toContain('9007199254740993');
    expect(formatted.text).toContain('<script>');
    expect(JSON.parse(formatted.text!)).toEqual(JSON.parse(value));
  });
  it('rejects truncated, malformed and excessive nesting without formatting', () => {
    expect(formatErrorJSON('{"partial":', true).reason).toBe('truncated');
    expect(formatErrorJSON('<html>error</html>', false).reason).toBe('invalid');
    expect(formatErrorJSON('['.repeat(65) + '0' + ']'.repeat(65), false).reason).toBe('depth');
    expect(formatErrorJSON('['.repeat(64) + '0' + ']'.repeat(64), false).text).toBeDefined();
  });
  it('does not inflate deeply indented large input without a bound', () => {
    const raw = '['.repeat(63) + '[' + '0,'.repeat(30_000) + '0]' + ']'.repeat(63);
    expect(formatErrorJSON(raw, false).reason).toBe('size');
  });
  it('decodes non UTF-8 original bytes without replacing them in downloads', () => {
    expect([
      ...errorBytes({ body: '/wD+', encoding: 'base64', bytes_saved: 3 } as ErrorBody),
    ]).toEqual([255, 0, 254]);
    expect(() =>
      errorBytes({ body: 'x', encoding: 'utf-8', bytes_saved: 1_048_577 } as ErrorBody),
    ).toThrow();
  });
});
