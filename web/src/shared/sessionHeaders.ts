import { ApiError } from '@shared/query/http';

export const SESSION_HEADER_FORMATS = [
  'uuid_v4',
  'hex_32',
  'opencode_session',
  'opencode_message',
  'opencode_part',
] as const;
export type SessionHeaderFormat = (typeof SESSION_HEADER_FORMATS)[number];
export const DEFAULT_SESSION_HEADER_IDLE = 300;
export interface SessionHeaderInput {
  name: string;
  idle_seconds?: number;
  format?: SessionHeaderFormat;
}
export interface SessionHeader extends SessionHeaderInput {
  idle_seconds: number;
  format: SessionHeaderFormat;
}
export type SessionHeaderDraft = Omit<SessionHeader, 'idle_seconds'> & {
  idle_seconds: number | '';
};

export function normalizeSessionHeaders(
  value: unknown,
  kind: 'response' | 'input' = 'response',
): SessionHeader[] {
  const invalid = (): never => {
    throw new ApiError(
      kind === 'input' ? 'invalid_request' : 'invalid_response',
      'Invalid session header configuration.',
      kind === 'input' ? 400 : 200,
    );
  };
  if (value === undefined && kind === 'response') return [];
  if (!Array.isArray(value) || value.length > 16) return invalid();
  return value.map((item: unknown) => {
    if (!item || typeof item !== 'object' || Array.isArray(item)) return invalid();
    const entry = item as Record<string, unknown>;
    if (Object.keys(entry).some((key) => !['name', 'idle_seconds', 'format'].includes(key)))
      return invalid();
    const idle =
      entry.idle_seconds === undefined ? DEFAULT_SESSION_HEADER_IDLE : entry.idle_seconds;
    const format = entry.format === undefined ? 'uuid_v4' : entry.format;
    if (
      typeof entry.name !== 'string' ||
      !entry.name.trim() ||
      entry.name.length > 128 ||
      typeof idle !== 'number' ||
      !Number.isInteger(idle) ||
      idle < 0 ||
      idle > 86400 ||
      !SESSION_HEADER_FORMATS.includes(format as SessionHeaderFormat)
    )
      return invalid();
    return { name: entry.name, idle_seconds: idle, format: format as SessionHeaderFormat };
  });
}

export const sessionHeaderDraft = (value: readonly SessionHeader[] = []): SessionHeaderDraft[] =>
  value.map((row) => ({ ...row }));
export const validSessionHeaderDraft = (value: readonly SessionHeaderDraft[]) =>
  value.every(
    (row) =>
      row.name.trim() &&
      (row.idle_seconds === '' ||
        (Number.isInteger(row.idle_seconds) && row.idle_seconds >= 0 && row.idle_seconds <= 86400)),
  );
export function buildSessionHeaders(value: readonly SessionHeaderDraft[]): SessionHeader[] {
  return normalizeSessionHeaders(
    value.map((row) => ({
      ...row,
      name: row.name.trim(),
      idle_seconds: row.idle_seconds === '' ? DEFAULT_SESSION_HEADER_IDLE : row.idle_seconds,
    })),
    'input',
  );
}
export function sameSessionHeaders(
  left: readonly (SessionHeaderInput | SessionHeaderDraft)[] = [],
  right: readonly (SessionHeaderInput | SessionHeaderDraft)[] = [],
): boolean {
  const signature = (values: readonly (SessionHeaderInput | SessionHeaderDraft)[]) =>
    values
      .map((row) => ({
        name: row.name.trim().toLowerCase(),
        idle_seconds:
          row.idle_seconds === ''
            ? DEFAULT_SESSION_HEADER_IDLE
            : (row.idle_seconds ?? DEFAULT_SESSION_HEADER_IDLE),
        format: row.format ?? 'uuid_v4',
      }))
      .sort((a, b) => a.name.localeCompare(b.name));
  return JSON.stringify(signature(left)) === JSON.stringify(signature(right));
}
