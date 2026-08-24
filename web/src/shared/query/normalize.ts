export type UnknownRecord = Record<string, unknown>;

export const MAX_LIST_ITEMS = 1000;

export interface ListResult {
  items: unknown[];
  hasNext: boolean;
}

export function asRecord(value: unknown): UnknownRecord | null {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
    ? (value as UnknownRecord)
    : null;
}

export function asArray(value: unknown): unknown[] {
  return Array.isArray(value) ? value : [];
}

/**
 * API list endpoints currently return arrays. Accept the optional `{data,
 * has_more}` envelope as well so pagination remains compatible with a server
 * that later adds an explicit cursor/page flag. The browser never invents
 * records; the inferred flag only controls whether the next page button is
 * available.
 */
export function isListPayload(value: unknown): boolean {
  const record = asRecord(value);
  return Array.isArray(value) || Boolean(record && Array.isArray(record.data));
}

export function listResult(value: unknown, pageSize = 20): ListResult {
  const record = asRecord(value);
  const rawItems = Array.isArray(value) ? value : record ? asArray(record.data) : [];
  const items = rawItems.slice(0, MAX_LIST_ITEMS);
  const explicitHasNext = record?.has_more ?? record?.hasNext ?? record?.next_page;
  const hasNext =
    typeof explicitHasNext === 'boolean'
      ? explicitHasNext
      : typeof explicitHasNext === 'number'
        ? explicitHasNext > 0
        : rawItems.length >= pageSize;
  return { items, hasNext };
}

export function cleanText(value: string): string {
  let cleaned = '';
  for (const character of value) {
    const codePoint = character.codePointAt(0) ?? 0;
    if (codePoint === 9 || codePoint === 10 || codePoint === 13) {
      cleaned += ' ';
    } else if (codePoint < 32 || codePoint === 127) {
      continue;
    } else {
      cleaned += character;
    }
  }
  return cleaned.trim();
}

export function hasControlCharacters(value: string): boolean {
  for (const character of value) {
    const codePoint = character.codePointAt(0) ?? 0;
    if (codePoint < 32 || codePoint === 127) return true;
  }
  return false;
}

/**
 * Keep resource identifiers opaque once they enter client state. JSON numbers
 * from the legacy alpha.2 wire are stringified only after a safe-integer
 * check; string identifiers are bounded and control-free but are never
 * trimmed, parsed, or otherwise interpreted here.
 */
export function opaqueID(value: unknown, max = 128): string | undefined {
  if (typeof value === 'number') {
    return Number.isSafeInteger(value) && value > 0 ? String(value) : undefined;
  }
  if (typeof value !== 'string' || value.length === 0 || value.length > max || hasControlCharacters(value)) {
    return undefined;
  }
  return value;
}

export function text(value: unknown, max = 256, fallback = ''): string {
  if (typeof value !== 'string') return fallback;
  const cleaned = cleanText(value);
  if (!cleaned) return fallback;
  const characters = Array.from(cleaned);
  return characters.length > max ? `${characters.slice(0, max).join('')}…` : cleaned;
}

export function optionalText(value: unknown, max = 256): string | undefined {
  const result = text(value, max);
  return result || undefined;
}

export function booleanValue(value: unknown, fallback = false): boolean {
  return typeof value === 'boolean' ? value : fallback;
}

export function numberValue(value: unknown, fallback = 0): number {
  return typeof value === 'number' && Number.isFinite(value) ? value : fallback;
}

export function integerValue(value: unknown, fallback = 0): number {
  const result = numberValue(value, fallback);
  return Number.isInteger(result) ? result : Math.trunc(result);
}

export function dateValue(value: unknown): string {
  if (typeof value === 'number' && Number.isFinite(value) && value > 0) {
    const milliseconds = value < 1_000_000_000_000 ? value * 1000 : value;
    const date = new Date(milliseconds);
    if (!Number.isNaN(date.getTime())) return date.toISOString();
  }
  return text(value, 64, '—');
}

export function idValue(value: unknown): string {
  if (typeof value === 'number' && Number.isSafeInteger(value) && value > 0) {
    return String(value);
  }
  return text(value, 128, '—');
}

/**
 * Validate a string at one of the frozen alpha.2 numeric JSON wire boundaries.
 * This helper is deliberately not used by domain/state ID decoders: opaque
 * string IDs must not be interpreted as numbers until the final serializer.
 */
export function positiveDecimalID(value: unknown): string | undefined {
  if (typeof value === 'number') {
    return Number.isSafeInteger(value) && value > 0 ? String(value) : undefined;
  }
  if (typeof value !== 'string' || !/^[1-9][0-9]*$/.test(value)) return undefined;
  if (value.length > 16) return undefined;
  try {
    const parsed = BigInt(value);
    return parsed > 0n && parsed <= BigInt(Number.MAX_SAFE_INTEGER) ? value : undefined;
  } catch {
    return undefined;
  }
}

/**
 * Encode a previously validated opaque resource id only at a legacy numeric
 * JSON boundary.  Callers must keep ids as canonical strings everywhere else
 * (state, cache keys, URLs and comparisons); this helper is intentionally
 * narrow so a permissive Number() conversion cannot leak into those paths.
 */
export function positiveDecimalIDNumber(value: unknown): number | undefined {
  const canonical = positiveDecimalID(value);
  if (!canonical) return undefined;
  const numeric = Number(canonical);
  return Number.isSafeInteger(numeric) && numeric > 0 ? numeric : undefined;
}

export function errorText(value: unknown, max = 4096): string {
  return text(value, max);
}
