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
  return text(value, 64, '—');
}

export function idValue(value: unknown): string {
  return text(value, 128, '—');
}

export function errorText(value: unknown, max = 4096): string {
  return text(value, max);
}
