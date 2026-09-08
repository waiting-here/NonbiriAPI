import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  createTimeDraft,
  editTimeDraft,
  fetchTimeZones,
  formatOffset,
  localInputText,
  rezoneTimeDraft,
  resolveLocalTime,
  timeDraftKey,
  timeDraftLocal,
  timeDraftValue,
} from '../../src/shared/time';
import { installJsonFetchFixtures } from './support';

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('time drafts', () => {
  const zone = 'America/New_York';
  const original = Date.parse('2026-11-01T05:30:47Z') / 1000;
  it('preserves an earlier fold instant and seconds when unchanged or restored', () => {
    const draft = createTimeDraft(original, 'minute', zone);
    expect(draft.text).toBe('2026-11-01T01:30');
    expect(timeDraftValue(draft, zone)).toBe(original);
    const edited = editTimeDraft(draft, '2026-11-01T01:31');
    expect(timeDraftValue(edited, zone)).toBeUndefined();
    expect(timeDraftValue(editTimeDraft(edited, '2026-11-01T01:30:00.000'), zone)).toBe(original);
    expect(timeDraftLocal(edited)).toBe('2026-11-01T01:31:00');
  });
  it('keeps nullable clearing separate from Unix zero and normalizes second inputs', () => {
    const draft = createTimeDraft(0, 'second', 'UTC');
    expect(timeDraftValue(draft, 'UTC')).toBe(0);
    expect(timeDraftValue(editTimeDraft(draft, ''), 'UTC')).toBeNull();
    expect(timeDraftValue(editTimeDraft(draft, '', true), 'UTC')).toBeUndefined();
    expect(timeDraftValue(editTimeDraft(draft, '1970-01-01T00:00'), 'UTC')).toBe(0);
    expect(timeDraftValue(editTimeDraft(draft, '1970-01-01T00:00:00.000'), 'UTC')).toBe(0);
  });
  it('rejects incomplete, impossible and fractional new input', () => {
    for (const text of [
      '2026-02-30T12:00',
      '2026-01-01T25:00',
      '2026-01-01T12:00:00.001',
      'not a date',
    ]) {
      const draft = editTimeDraft(createTimeDraft(null, 'minute', 'UTC'), text);
      expect(timeDraftLocal(draft)).toBeUndefined();
      expect(timeDraftValue(draft, 'UTC')).toBeUndefined();
    }
  });
  it('invalidates resolved input when zone changes, re-formats originals and requires fallback confirmation', () => {
    const originalDraft = createTimeDraft(original, 'minute', zone);
    expect(timeDraftValue(originalDraft, 'Asia/Tokyo')).toBeUndefined();
    const rezoned = rezoneTimeDraft(originalDraft, 'Asia/Tokyo', true);
    expect(rezoned.text).toBe('2026-11-01T14:30');
    expect(rezoned.zoneChanged).toBe(true);
    expect(timeDraftValue(rezoned, 'Asia/Tokyo')).toBe(original);
    const edited = editTimeDraft(originalDraft, '2026-11-01T01:31');
    const fallback = rezoneTimeDraft(edited, 'Unknown/Zone', false);
    expect(fallback.text).toBe(edited.text);
    expect(fallback.zone).toBe('UTC');
    expect(fallback.needsUTCConfirmation).toBe(true);
    const confirmed = { ...fallback, needsUTCConfirmation: false };
    expect(rezoneTimeDraft(confirmed, 'Unknown/Zone', false)).toBe(confirmed);
    expect(timeDraftKey(confirmed)).not.toBe(timeDraftKey(edited));
  });
  it('formats half-hour zones, seconds and historical offset seconds', () => {
    expect(localInputText(1781526607, 'Asia/Kolkata', 'second')).toBe('2026-06-15T18:00:07');
    expect(formatOffset(-12600)).toBe('-03:30');
    expect(formatOffset(20776)).toBe('+05:46:16');
  });
});

describe('same-station time responses', () => {
  it.each(['user', 'admin'] as const)('checks %s response fields and paths', async (station) => {
    const base = station === 'admin' ? '/admin/api' : '/api';
    const local = '2026-03-08T02:30:00';
    const params = new URLSearchParams({ local, time_zone: 'America/New_York' });
    installJsonFetchFixtures([
      {
        method: 'GET',
        path: `${base}/time-zones`,
        body: { version: 'go1.26.6-zoneinfo', zones: ['America/New_York', 'UTC'] },
      },
      {
        method: 'GET',
        path: `${base}/time/resolve?${params}`,
        body: {
          instant: 1772955000,
          local: '2026-03-08T03:30:00',
          time_zone: 'America/New_York',
          offset_seconds: -14400,
          adjustment: 'gap_shifted',
        },
      },
    ]);
    expect((await fetchTimeZones(station)).zones).toContain('UTC');
    expect((await resolveLocalTime(station, local, 'America/New_York')).instant).toBe(1772955000);
  });
  it.each([
    { instant: -1 },
    { instant: 1.5 },
    { instant: 253402300800 },
    { local: '2026-02-30T12:00:00' },
    { offset_seconds: 86400 },
    { time_zone: 'Asia/Tokyo' },
    { adjustment: 'other' },
    { instant: 1 },
  ])('rejects an inconsistent response: %j', async (invalid) => {
    const local = '1970-01-01T00:00:00';
    installJsonFetchFixtures([
      {
        method: 'GET',
        path: `/api/time/resolve?${new URLSearchParams({ local, time_zone: 'UTC' })}`,
        body: {
          instant: 0,
          local,
          time_zone: 'UTC',
          offset_seconds: 0,
          adjustment: 'none',
          ...invalid,
        },
      },
    ]);
    await expect(resolveLocalTime('user', local, 'UTC')).rejects.toThrow();
  });
});
