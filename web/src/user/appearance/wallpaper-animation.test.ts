import { describe, expect, it } from 'vitest';
import {
  blinkAt,
  celestialRects,
  PaintCadence,
  whaleCycleSeconds,
  whaleRect,
} from './wallpaper-animation';

describe('wallpaper timing', () => {
  it.each([60, 90, 120, 144])('keeps an even 60 Hz budget on a %i Hz display', (rate) => {
    const clock = new PaintCadence();
    const paints: number[] = [];
    for (let i = 0; i < rate * 10; i++) {
      const at = (i * 1000) / rate + Math.sin(i * 0.73) * 0.15;
      if (clock.due(at)) paints.push(at);
    }
    expect(paints.length).toBeGreaterThanOrEqual(599);
    expect(paints.length).toBeLessThanOrEqual(601);
    expect(Math.max(...paints.slice(1).map((t, i) => t - paints[i]))).toBeLessThan(
      1000 / 60 + 1000 / rate + 1,
    );
  });

  it('resumes without accumulating a backlog after a stalled or hidden frame', () => {
    const clock = new PaintCadence();
    expect(clock.due(0)).toBe(true);
    expect(clock.due(2000)).toBe(true);
    expect(clock.due(2001)).toBe(false);
    expect(clock.due(2017)).toBe(true);
    clock.reset();
    expect(clock.due(5000)).toBe(true);
    expect(clock.due(5001)).toBe(false);
  });

  it('closes continuously and reopens more slowly without a loop seam', () => {
    const samples = Array.from({ length: 29 }, (_, i) => blinkAt(2.2 + i / 60));
    expect(samples.filter((v) => v > 0 && v < 1).length).toBeGreaterThan(16);
    expect(samples[0]).toBe(0);
    expect(Math.max(...samples)).toBe(1);
    expect(samples.at(-1)).toBe(0);
    expect(Math.max(...samples.slice(1).map((v, i) => Math.abs(v - samples[i])))).toBeLessThan(0.2);
    expect(blinkAt(17.6 - 0.001)).toBe(blinkAt(17.6 + 0.001));
  });
});

it('keeps the entire whale outside the viewport on either side of its wrap', () => {
  const wrap = whaleCycleSeconds * (1 - 0.19);
  for (const [w, h] of [
    [1440, 900],
    [390, 844],
    [3840, 2160],
  ]) {
    const before = whaleRect(wrap - 0.001, w, h),
      after = whaleRect(wrap + 0.001, w, h);
    expect(before[0] + before[2]).toBeLessThan(-0.15);
    expect(after[0]).toBeGreaterThan(1.15);
  }
});

it('keeps celestial bodies separated and continuous at the orbit seam', () => {
  for (let t = 0; t <= 420; t++) {
    const [moon, earth] = celestialRects(t);
    expect(moon[0] - (earth[0] + earth[2])).toBeGreaterThan(0.07);
  }
  const start = celestialRects(0),
    end = celestialRects(420);
  for (let body = 0; body < 2; body++)
    for (let n = 0; n < 4; n++) expect(start[body][n]).toBeCloseTo(end[body][n], 10);
});

it('keeps a gentle whale crossing speed on both screen shapes', () => {
  for (const [w, h] of [
    [1440, 900],
    [390, 844],
  ]) {
    const a = whaleRect(25, w, h),
      b = whaleRect(26, w, h);
    expect(a[0] - b[0]).toBeGreaterThan(0);
    expect(a[0] - b[0]).toBeLessThan(0.036);
  }
});
