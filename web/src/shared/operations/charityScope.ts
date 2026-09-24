import { decimalID } from './wire';

export function charityScopePath(path: string, modelID?: string): string {
  if (!modelID) return path;
  const separator = path.includes('?') ? '&' : '?';
  return `${path}${separator}charity_model_id=${decimalID(modelID, 'charity model context')}`;
}

export const protectedRequestFields = new Set([
  'model',
  'messages',
  'input',
  'stream',
  'stream_options',
  'tools',
  'tool_choice',
  'functions',
  'function_call',
  'response_format',
  'encoding_format',
]);

export function excludedFields(value: string): string[] | null {
  const fields = [...new Set(value.split(/[\s,]+/).filter(Boolean))];
  if (
    fields.length > 32 ||
    fields.some(
      (field) => !/^[A-Za-z_][A-Za-z0-9_]{0,63}$/.test(field) || protectedRequestFields.has(field),
    )
  )
    return null;
  return fields;
}

export function halfPrice(value: string): string | null {
  if (!/^(0|[1-9][0-9]{0,15})(\.[0-9]{0,2}[1-9])?$/.test(value)) return null;
  const [whole, fraction = ''] = value.split('.');
  const milli = BigInt(whole) * 1000n + BigInt(fraction.padEnd(3, '0'));
  if (milli > 9_000_000_000_000_000n) return null;
  const half = milli / 2n;
  const remainder = String(half % 1000n)
    .padStart(3, '0')
    .replace(/0+$/, '');
  return remainder ? `${half / 1000n}.${remainder}` : String(half / 1000n);
}
