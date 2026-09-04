import { describe, expect, it } from 'vitest';
import { announcementExpiryInput, announcementExpiryWire } from './announcements';

describe('announcement datetime-local conversion', () => {
  it('formats the same epoch for standard and daylight offsets without a UTC shift', () => {
    const epoch = Date.UTC(2026, 0, 15, 18, 30) / 1_000;
    expect(announcementExpiryInput(epoch, 300)).toBe('2026-01-15T13:30');
    expect(announcementExpiryInput(epoch, 240)).toBe('2026-01-15T14:30');
  });

  it('round-trips the browser local wall clock and preserves null', () => {
    const epoch = 1_798_820_640;
    expect(announcementExpiryWire(announcementExpiryInput(epoch))).toBe(epoch - (epoch % 60));
    expect(announcementExpiryWire('')).toBeNull();
  });
});
