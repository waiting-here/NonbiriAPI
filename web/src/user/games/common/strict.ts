import { ApiError } from '@shared/query/http';

const MAX_TIME = 253_402_300_799;
const U128_MAX = (1n << 128n) - 1n;
const U256_MAX = (1n << 256n) - 1n;
const SM128_MAG_MAX = (1n << 127n) - 1n;
const UNSIGNED_DECIMAL = /^(0|[1-9][0-9]*)$/;
const SIGNED_DECIMAL = /^-?(0|[1-9][0-9]*)$/;
const CREDIT_AMOUNT = /^(0|[1-9][0-9]*)(?:\.([0-9]{1,3}))?$/;
const SIGNED_CREDIT_AMOUNT = /^-?(0|[1-9][0-9]*)(?:\.([0-9]{1,3}))?$/;
const BASE64URL = /^[A-Za-z0-9_-]+$/;

export function invalidResponse(field: string): never {
  throw new ApiError('invalid_response', `The server returned invalid game data (${field}).`, 200);
}

export function exactRecord(
  value: unknown,
  required: readonly string[],
  optional: readonly string[] = [],
  field = 'object',
): Record<string, unknown> {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) invalidResponse(field);
  const record = value as Record<string, unknown>;
  const allowed = new Set([...required, ...optional]);
  const keys = Object.keys(record);
  if (
    keys.some((key) => !allowed.has(key)) ||
    required.some((key) => !Object.prototype.hasOwnProperty.call(record, key))
  ) {
    invalidResponse(field);
  }
  return record;
}

export function booleanValue(value: unknown, field: string): boolean {
  if (typeof value !== 'boolean') invalidResponse(field);
  return value;
}

export function safeInteger(
  value: unknown,
  minimum: number,
  maximum: number,
  field: string,
): number {
  if (!Number.isSafeInteger(value) || (value as number) < minimum || (value as number) > maximum) {
    invalidResponse(field);
  }
  return value as number;
}

export function unixTime(value: unknown, field: string): number {
  return safeInteger(value, 0, MAX_TIME, field);
}

export function enumValue<const T extends readonly string[]>(
  value: unknown,
  values: T,
  field: string,
): T[number] {
  if (typeof value !== 'string' || !(values as readonly string[]).includes(value))
    invalidResponse(field);
  return value as T[number];
}

export function decimalValue(
  value: unknown,
  options: { bits: 128 | 256; positive?: boolean },
  field: string,
): string {
  if (typeof value !== 'string' || !UNSIGNED_DECIMAL.test(value)) invalidResponse(field);
  const parsed = BigInt(value);
  if (
    (options.positive && parsed === 0n) ||
    parsed > (options.bits === 128 ? U128_MAX : U256_MAX)
  ) {
    invalidResponse(field);
  }
  return value;
}

export function signedDecimalValue(value: unknown, field: string): string {
  if (typeof value !== 'string' || !SIGNED_DECIMAL.test(value) || value === '-0')
    invalidResponse(field);
  const parsed = BigInt(value);
  if (parsed < -SM128_MAG_MAX || parsed > SM128_MAG_MAX) invalidResponse(field);
  return value;
}

function parseCredits(value: string, signed: boolean, bits: 128 | 256): bigint {
  const pattern = signed ? SIGNED_CREDIT_AMOUNT : CREDIT_AMOUNT;
  if (!pattern.test(value) || value === '-0' || (value.endsWith('0') && value.includes('.'))) {
    invalidResponse('amount');
  }
  const negative = value.startsWith('-');
  const unsigned = negative ? value.slice(1) : value;
  const [whole, fraction = ''] = unsigned.split('.');
  const milli = BigInt(whole) * 1000n + BigInt((fraction + '000').slice(0, 3));
  const maximum = signed ? SM128_MAG_MAX : bits === 128 ? U128_MAX : U256_MAX;
  if (milli > maximum) invalidResponse('amount range');
  return negative ? -milli : milli;
}

export function creditsValue(
  value: unknown,
  options: { signed?: boolean; positive?: boolean; bits?: 128 | 256 } = {},
  field = 'amount',
): string {
  if (typeof value !== 'string') invalidResponse(field);
  let parsed: bigint;
  try {
    parsed = parseCredits(value, options.signed ?? false, options.bits ?? 128);
  } catch (error) {
    if (error instanceof ApiError)
      throw new ApiError(error.code, `The server returned invalid game data (${field}).`, 200);
    invalidResponse(field);
  }
  if (options.positive && parsed <= 0n) invalidResponse(field);
  return value;
}

export function creditsToMilli(value: string, signed = false, bits: 128 | 256 = 128): bigint {
  return parseCredits(value, signed, bits);
}

export function creditsFromMilli(value: bigint): string {
  const negative = value < 0n;
  const absolute = negative ? -value : value;
  const whole = absolute / 1000n;
  const fraction = (absolute % 1000n).toString().padStart(3, '0').replace(/0+$/, '');
  return `${negative ? '-' : ''}${whole}${fraction ? `.${fraction}` : ''}`;
}

export function multiplyCredits(value: string, count: number): string {
  return creditsFromMilli(creditsToMilli(value) * BigInt(count));
}

export function sumCredits(values: readonly string[], signed = false): string {
  return creditsFromMilli(values.reduce((sum, value) => sum + creditsToMilli(value, signed), 0n));
}

function groupedInteger(value: string): string {
  let result = '';
  for (let index = 0; index < value.length; index += 1) {
    if (index > 0 && (value.length - index) % 3 === 0) result += ',';
    result += value[index];
  }
  return result;
}

export function formatCredits(value: string): string {
  const negative = value.startsWith('-');
  const unsigned = negative ? value.slice(1) : value;
  const [whole, fraction] = unsigned.split('.');
  return `${negative ? '-' : ''}${groupedInteger(whole)}${fraction ? `.${fraction}` : ''}`;
}

export function textValue(value: unknown, maximumRunes: number, field: string): string {
  if (typeof value !== 'string' || value.length === 0 || Array.from(value).length > maximumRunes) {
    invalidResponse(field);
  }
  for (const character of value) {
    const point = character.codePointAt(0) ?? 0;
    if (point === 0 || point <= 0x1f || point === 0x7f || (point >= 0x80 && point <= 0x9f)) {
      invalidResponse(field);
    }
  }
  return value;
}

export function httpsURL(value: unknown, field: string): string | null {
  if (value === null) return null;
  if (typeof value !== 'string' || value.length > 2048) invalidResponse(field);
  try {
    const parsed = new URL(value);
    if (parsed.protocol !== 'https:' || parsed.username || parsed.password) invalidResponse(field);
    return value;
  } catch {
    invalidResponse(field);
  }
}

export function encodeBase64URL(bytes: Uint8Array): string {
  let binary = '';
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

export function decodeBase64URL(value: string, expectedBytes: number): Uint8Array | null {
  if (!BASE64URL.test(value) || value.includes('=')) return null;
  const padding = '='.repeat((4 - (value.length % 4)) % 4);
  try {
    const binary = atob(value.replace(/-/g, '+').replace(/_/g, '/') + padding);
    const bytes = Uint8Array.from(binary, (character) => character.charCodeAt(0));
    if (bytes.length !== expectedBytes || encodeBase64URL(bytes) !== value) return null;
    return bytes;
  } catch {
    return null;
  }
}

export function opaqueID(value: unknown, prefix: string, field: string): string {
  if (typeof value !== 'string' || !value.startsWith(prefix)) invalidResponse(field);
  const body = value.slice(prefix.length);
  if (body.length !== 22 || decodeBase64URL(body, 16) === null) invalidResponse(field);
  return value;
}

export type PublicIdentity =
  | { readonly kind: 'anonymous' }
  | { readonly kind: 'public'; readonly displayName: string; readonly avatarURL: string | null };

export function publicIdentity(value: unknown, field: string): PublicIdentity {
  const discriminator = exactRecord(value, ['kind'], ['display_name', 'avatar_url'], field);
  if (discriminator.kind === 'anonymous') {
    exactRecord(value, ['kind'], [], field);
    return { kind: 'anonymous' };
  }
  if (discriminator.kind !== 'public') invalidResponse(field);
  const record = exactRecord(value, ['kind', 'display_name', 'avatar_url'], [], field);
  return {
    kind: 'public',
    displayName: textValue(record.display_name, 128, `${field} display name`),
    avatarURL: httpsURL(record.avatar_url, `${field} avatar`),
  };
}
