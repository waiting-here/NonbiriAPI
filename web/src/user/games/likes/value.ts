import { exactRecord, invalidResponse, safeInteger, textValue } from '../common/strict';
import { list } from '../common/duel/normalize';

export const amount = (value: unknown) => safeInteger(value, 0, 1_000_000_000, 'resource');
export const signedAmount = (value: unknown) =>
  safeInteger(value, -1_000_000_000, 1_000_000_000, 'change');
export const label = (value: unknown) => textValue(value, 160, 'identifier');
export function prose(value: unknown, maximum = 8000): string {
  if (value === '') return '';
  return textValue(value, maximum, 'description');
}
export function dictionary<T>(
  value: unknown,
  decode: (v: unknown) => T,
  maximum = 256,
): Record<string, T> {
  if (value === null || typeof value !== 'object' || Array.isArray(value))
    invalidResponse('dictionary');
  const keys = Object.keys(value);
  if (keys.length > maximum) invalidResponse('dictionary size');
  const r = exactRecord(value, keys),
    result: Record<string, T> = {};
  for (const key of keys) {
    label(key);
    if (key === '__proto__' || key === 'constructor' || key === 'prototype')
      invalidResponse('dictionary key');
    result[key] = decode(r[key]);
  }
  return result;
}
export function unique<T>(
  value: unknown,
  maximum: number,
  decode: (v: unknown) => T,
  key: (v: T) => string,
): T[] {
  const items = list(value, maximum, decode);
  if (new Set(items.map(key)).size !== items.length) invalidResponse('duplicate identifier');
  return items;
}
export type JSONValue =
  null | string | number | boolean | JSONValue[] | { [key: string]: JSONValue };
export function safeJSON(value: unknown, depth = 0, budget = { remaining: 20000 }): JSONValue {
  if (depth > 12 || --budget.remaining < 0) invalidResponse('event size');
  if (value === null || typeof value === 'boolean') return value;
  if (typeof value === 'number') return signedAmount(value);
  if (typeof value === 'string') return prose(value, 16000);
  if (Array.isArray(value)) return list(value, 512, (v) => safeJSON(v, depth + 1, budget));
  return dictionary(value, (v) => safeJSON(v, depth + 1, budget), 256);
}
