import { apiFetch } from './query/http';

export type TimeStation = 'user' | 'admin';
export type TimePrecision = 'minute' | 'second';
export interface TimeZoneRegistry {
  version: string;
  zones: string[];
}
export interface ResolvedTime {
  instant: number;
  local: string;
  time_zone: string;
  offset_seconds: number;
  adjustment: 'none' | 'gap_shifted' | 'fold_later';
}

const MAX_INSTANT = 253402300799;
const LOCAL_SECONDS = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}$/;
const prefix = (station: TimeStation) => (station === 'admin' ? '/admin/api' : '/api');
const invalidResponse = () => new Error('Invalid time response');
const object = (value: unknown): value is Record<string, unknown> =>
  !!value && typeof value === 'object' && !Array.isArray(value);

// UTC parsing here only validates calendar text and response arithmetic. The
// server remains the sole authority for resolving a wall clock in a zone.
function wallSeconds(local: string): number | undefined {
  if (!LOCAL_SECONDS.test(local)) return undefined;
  const millis = Date.parse(`${local}Z`);
  if (!Number.isFinite(millis) || new Date(millis).toISOString().slice(0, 19) !== local)
    return undefined;
  return millis / 1000;
}

export async function fetchTimeZones(
  station: TimeStation,
  signal?: AbortSignal,
): Promise<TimeZoneRegistry> {
  const value = await apiFetch<unknown>(`${prefix(station)}/time-zones`, { signal });
  if (
    !object(value) ||
    value.version !== 'go1.26.6-zoneinfo' ||
    !Array.isArray(value.zones) ||
    !value.zones.includes('UTC') ||
    value.zones.length > 2048
  )
    throw invalidResponse();
  const zones: string[] = [];
  for (const zone of value.zones) {
    if (
      typeof zone !== 'string' ||
      !/^[A-Za-z0-9_+/-]{1,64}$/.test(zone) ||
      zone === 'Local' ||
      zone.startsWith('/') ||
      zone.includes('..') ||
      (zones.length > 0 && zones[zones.length - 1] >= zone)
    )
      throw invalidResponse();
    zones.push(zone);
  }
  return { version: value.version, zones };
}

export async function resolveLocalTime(
  station: TimeStation,
  local: string,
  zone: string,
  signal?: AbortSignal,
): Promise<ResolvedTime> {
  if (wallSeconds(local) === undefined) throw new Error('Invalid local time');
  const params = new URLSearchParams({ local, time_zone: zone });
  const value = await apiFetch<unknown>(`${prefix(station)}/time/resolve?${params}`, { signal });
  if (
    !object(value) ||
    typeof value.instant !== 'number' ||
    !Number.isSafeInteger(value.instant) ||
    value.instant < 0 ||
    value.instant > MAX_INSTANT ||
    value.time_zone !== zone ||
    typeof value.local !== 'string' ||
    typeof value.offset_seconds !== 'number' ||
    !Number.isInteger(value.offset_seconds) ||
    Math.abs(value.offset_seconds) >= 86400 ||
    !['none', 'gap_shifted', 'fold_later'].includes(String(value.adjustment)) ||
    wallSeconds(value.local) !== value.instant + value.offset_seconds ||
    (value.adjustment === 'gap_shifted' ? value.local <= local : value.local !== local)
  )
    throw invalidResponse();
  return value as unknown as ResolvedTime;
}

export function browserTimeZone(): string | null {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || null;
  } catch {
    return null;
  }
}

export function formatOffset(seconds: number): string {
  const absolute = Math.abs(seconds);
  const pad = (number: number) => String(number).padStart(2, '0');
  return `${seconds < 0 ? '-' : '+'}${pad(Math.floor(absolute / 3600))}:${pad(Math.floor((absolute % 3600) / 60))}${absolute % 60 ? `:${pad(absolute % 60)}` : ''}`;
}

export function localInputText(
  epoch: number | null,
  zone: string,
  precision: TimePrecision = 'minute',
): string {
  if (epoch === null) return '';
  const date = new Date(epoch * 1000);
  try {
    const parts = new Intl.DateTimeFormat('en-CA', {
      timeZone: zone,
      calendar: 'gregory',
      numberingSystem: 'latn',
      hourCycle: 'h23',
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
    }).formatToParts(date);
    const get = (type: Intl.DateTimeFormatPartTypes) =>
      parts.find((part) => part.type === type)?.value ?? '';
    return `${get('year').padStart(4, '0')}-${get('month')}-${get('day')}T${get('hour')}:${get('minute')}${precision === 'second' ? `:${get('second')}` : ''}`;
  } catch {
    // Even a browser without Intl can display the explicitly confirmed UTC fallback.
    return date.toISOString().slice(0, precision === 'second' ? 19 : 16);
  }
}

function canonicalText(value: string, precision: TimePrecision): string {
  let text = value.replace(/(T\d{2}:\d{2}:\d{2})\.0+$/, '$1');
  if (precision === 'minute') text = text.replace(/(T\d{2}:\d{2}):00$/, '$1');
  else if (/T\d{2}:\d{2}$/.test(text)) text += ':00';
  return text;
}

export interface TimeDraft {
  originalEpoch: number | null;
  originalText: string;
  text: string;
  browserZone: string | null;
  zone: string;
  precision: TimePrecision;
  needsUTCConfirmation: boolean;
  zoneChanged: boolean;
  invalidInput: boolean;
  resolved?: { key: string; value: ResolvedTime };
  errorKey?: string;
}

export function createTimeDraft(
  epoch: number | null = null,
  precision: TimePrecision = 'minute',
  browserZone = browserTimeZone(),
): TimeDraft {
  const zone = browserZone ?? 'UTC';
  const text = localInputText(epoch, zone, precision);
  return {
    originalEpoch: epoch,
    originalText: text,
    text,
    browserZone,
    zone,
    precision,
    needsUTCConfirmation: !browserZone,
    zoneChanged: false,
    invalidInput: false,
  };
}

export function editTimeDraft(draft: TimeDraft, text: string, invalidInput = false): TimeDraft {
  return {
    ...draft,
    text: canonicalText(text, draft.precision),
    invalidInput,
    resolved: undefined,
    errorKey: undefined,
  };
}

export function rezoneTimeDraft(
  draft: TimeDraft,
  browserZone: string | null,
  supported: boolean,
): TimeDraft {
  const zone = supported && browserZone ? browserZone : 'UTC';
  if (draft.browserZone === browserZone && draft.zone === zone) return draft;
  const originalText = localInputText(draft.originalEpoch, zone, draft.precision);
  return {
    ...draft,
    browserZone,
    zone,
    originalText,
    text: draft.text === draft.originalText ? originalText : draft.text,
    needsUTCConfirmation: !supported,
    zoneChanged: draft.zoneChanged || draft.browserZone !== browserZone || draft.zone !== zone,
    resolved: undefined,
    errorKey: undefined,
  };
}

export function timeDraftLocal(draft: TimeDraft): string | undefined {
  if (draft.invalidInput) return undefined;
  const local = draft.precision === 'minute' ? `${draft.text}:00` : draft.text;
  return wallSeconds(local) === undefined ? undefined : local;
}

export function timeDraftKey(draft: TimeDraft): string {
  return `${draft.zone}|${draft.text}`;
}

// undefined means unresolved/invalid; null retains the field's empty semantics.
export function timeDraftValue(
  draft: TimeDraft,
  currentBrowserZone = browserTimeZone(),
): number | null | undefined {
  if (draft.invalidInput || draft.browserZone !== currentBrowserZone) return undefined;
  if (draft.text === draft.originalText) return draft.originalEpoch;
  if (draft.text === '') return null;
  if (draft.needsUTCConfirmation) return undefined;
  return draft.resolved?.key === timeDraftKey(draft) ? draft.resolved.value.instant : undefined;
}

export type TimeDraftUpdate = (current: TimeDraft) => TimeDraft;
