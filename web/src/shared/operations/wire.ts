import { ApiError } from '@shared/query/http';

export type WireRecord = Record<string, unknown>;

const MAX_UNIX_SECOND = 253_402_300_799;
const U128_MAX = (1n << 128n) - 1n;
const SM128_MAX = (1n << 127n) - 1n;

export function invalidResponse(label = 'response'): never {
  throw new ApiError('invalid_response', `The server returned an invalid ${label}.`, 200);
}

export function record(
  value: unknown,
  allowed: readonly string[],
  label: string,
  required: readonly string[] = allowed,
): WireRecord {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) invalidResponse(label);
  const result = value as WireRecord;
  const allowedSet = new Set(allowed);
  if (Object.keys(result).some((key) => !allowedSet.has(key))) invalidResponse(label);
  if (required.some((key) => !Object.prototype.hasOwnProperty.call(result, key))) invalidResponse(label);
  return result;
}

export function array(value: unknown, label: string, maximum = 10_000): unknown[] {
  if (!Array.isArray(value) || value.length > maximum) invalidResponse(label);
  return value;
}

function containsForbiddenControl(value: string, multiline: boolean): boolean {
  for (const character of value) {
    const code = character.codePointAt(0) ?? 0;
    if (code === 0x7f || (code < 0x20 && (!multiline || (code !== 0x09 && code !== 0x0a && code !== 0x0d)))) {
      return true;
    }
  }
  return false;
}

export function string(
  value: unknown,
  label: string,
  options: { min?: number; max?: number; bytes?: number; multiline?: boolean; ascii?: boolean } = {},
): string {
  if (typeof value !== 'string') invalidResponse(label);
  const { min = 0, max = 4_096, bytes = Number.POSITIVE_INFINITY, multiline = false, ascii = false } = options;
  const runes = Array.from(value);
  if (runes.length < min || runes.length > max || new TextEncoder().encode(value).byteLength > bytes) invalidResponse(label);
  if (containsForbiddenControl(value, multiline)) invalidResponse(label);
  if (ascii && runes.some((character) => (character.codePointAt(0) ?? 0) > 0x7e)) invalidResponse(label);
  return value;
}

export function nullableString(
  value: unknown,
  label: string,
  options?: Parameters<typeof string>[2],
): string | null {
  return value === null ? null : string(value, label, options);
}

export function boolean(value: unknown, label: string): boolean {
  if (typeof value !== 'boolean') invalidResponse(label);
  return value;
}

export function integer(value: unknown, label: string, minimum = 0, maximum = Number.MAX_SAFE_INTEGER): number {
  if (!Number.isSafeInteger(value) || (value as number) < minimum || (value as number) > maximum) invalidResponse(label);
  return value as number;
}

export function nullableInteger(
  value: unknown,
  label: string,
  minimum = 0,
  maximum = Number.MAX_SAFE_INTEGER,
): number | null {
  return value === null ? null : integer(value, label, minimum, maximum);
}

export function unixSecond(value: unknown, label: string): number {
  return integer(value, label, 0, MAX_UNIX_SECOND);
}

export function nullableUnixSecond(value: unknown, label: string): number | null {
  return value === null ? null : unixSecond(value, label);
}

export function oneOf<const T extends string>(value: unknown, values: readonly T[], label: string): T {
  if (typeof value !== 'string' || !values.includes(value as T)) invalidResponse(label);
  return value as T;
}

export function decimal(value: unknown, label: string, options: { positive?: boolean; u128?: boolean } = {}): string {
  if (typeof value !== 'string' || !/^(0|[1-9][0-9]*)$/.test(value)) invalidResponse(label);
  let parsed: bigint;
  try {
    parsed = BigInt(value);
  } catch {
    return invalidResponse(label);
  }
  if (options.positive && parsed === 0n) invalidResponse(label);
  if ((options.u128 ?? true) && parsed > U128_MAX) invalidResponse(label);
  return value;
}

export function signedDecimal(value: unknown, label: string): string {
  if (typeof value !== 'string' || !/^-?(0|[1-9][0-9]*)$/.test(value) || value === '-0') invalidResponse(label);
  let parsed: bigint;
  try {
    parsed = BigInt(value);
  } catch {
    return invalidResponse(label);
  }
  if (parsed < -SM128_MAX || parsed > SM128_MAX) invalidResponse(label);
  return value;
}

/** Canonical credit string: signed integer with at most three fractional digits. */
export function amount(value: unknown, label: string, signed = true): string {
  if (typeof value !== 'string' || !/^-?(0|[1-9][0-9]*)(\.[0-9]{1,3})?$/.test(value)
    || /^-0(?:\.0{1,3})?$/.test(value) || (value.includes('.') && value.endsWith('0'))) {
    invalidResponse(label);
  }
  if (!signed && value.startsWith('-')) invalidResponse(label);
  const negative = value.startsWith('-');
  const unsigned = negative ? value.slice(1) : value;
  const [whole, fraction = ''] = unsigned.split('.');
  const magnitude = BigInt(whole) * 1_000n + BigInt(fraction.padEnd(3, '0') || '0');
  const milli = negative ? -magnitude : magnitude;
  if (milli < -SM128_MAX || milli > SM128_MAX) invalidResponse(label);
  return value;
}

export function nullableDecimal(value: unknown, label: string): string | null {
  return value === null ? null : decimal(value, label);
}

export function decimalID(value: unknown, label: string): string {
  return decimal(value, label, { positive: true, u128: false });
}

export function nullableDecimalID(value: unknown, label: string): string | null {
  return value === null ? null : decimalID(value, label);
}

export function opaqueID(value: unknown, prefix: string, label: string): string {
  if (typeof value !== 'string' || value.length !== prefix.length + 22 || !value.startsWith(prefix)) invalidResponse(label);
  const suffix = value.slice(prefix.length);
  if (!/^[A-Za-z0-9_-]{22}$/.test(suffix) || !/[AQgw]$/.test(suffix)) invalidResponse(label);
  return value;
}

export function nullableOpaqueID(value: unknown, prefix: string, label: string): string | null {
  return value === null ? null : opaqueID(value, prefix, label);
}

export interface CursorPage<T> {
  data: T[];
  next_cursor: string | null;
}

/** A canonical, unpadded raw-base64url pagination cursor. */
export function cursor(value: unknown, label = 'cursor'): string | null {
  if (value === null) return null;
  const token = string(value, label, { min: 1, max: 512, bytes: 512, ascii: true });
  if (!/^[A-Za-z0-9_-]+$/.test(token) || token.length % 4 === 1) invalidResponse(label);

  let canonical: string;
  try {
    const padded = token.replaceAll('-', '+').replaceAll('_', '/')
      + '='.repeat((4 - token.length % 4) % 4);
    canonical = globalThis.btoa(globalThis.atob(padded))
      .replaceAll('+', '-')
      .replaceAll('/', '_')
      .replace(/=+$/, '');
  } catch {
    return invalidResponse(label);
  }
  if (canonical !== token) invalidResponse(label);
  return token;
}

export function page<T>(value: unknown, label: string, item: (entry: unknown) => T): CursorPage<T> {
  const root = record(value, ['data', 'next_cursor'], label);
  return {
    data: array(root.data, `${label} data`).map(item),
    next_cursor: cursor(root.next_cursor, `${label} cursor`),
  };
}

export function optionalField<T>(root: WireRecord, key: string, decode: (value: unknown) => T): T | undefined {
  return Object.prototype.hasOwnProperty.call(root, key) ? decode(root[key]) : undefined;
}

export function assertNever(value: never): never {
  throw new Error(`Unhandled closed value: ${String(value)}`);
}
