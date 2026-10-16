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
  it.each(['utf-8', 'base64'] as const)(
    'decodes %s redaction without comparing stored bytes',
    (encoding) => {
      const value = 'Failed at [redacted]';
      const body = encoding === 'utf-8' ? value : btoa(value);
      expect(
        new TextDecoder().decode(errorBytes({ body, encoding, bytes_saved: 25 } as ErrorBody)),
      ).toBe(value);
    },
  );
  it('decodes non UTF-8 bytes and bounds the displayed data', () => {
    expect([
      ...errorBytes({ body: '/wD+', encoding: 'base64', bytes_saved: 3 } as ErrorBody),
    ]).toEqual([255, 0, 254]);
    expect(() =>
      errorBytes({ body: 'x'.repeat(2_097_153), encoding: 'utf-8', bytes_saved: 1 } as ErrorBody),
    ).toThrow();
  });
});
