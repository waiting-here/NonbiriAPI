import { describe, expect, it } from 'vitest';
import { interpolate, overloadCues, settlementFrame } from './motion';
import { presentationValue, startEvents } from './normalize';
import wire from './testdata/authority.json';

describe('authoritative paired presentation', () => {
  it('shows only the overloaded seat as soon as the authoritative payment event is revealed', () => {
    const p = presentationValue(wire.scenarios.single_overload.summary);
    expect(overloadCues(p, 'reveal', false)).toEqual([false, false]);
    expect(overloadCues(p, 'payment', false)).toEqual([false, true]);
    expect(overloadCues(p, 'score', false)).toEqual([false, true]);
    expect(overloadCues(p, 'reveal', true)).toEqual([false, true]);
  });
  it('uses both players at the same semantic stage with real intermediate resource values', () => {
    const summary = presentationValue(wire.rounds[1].summary);
    const early = settlementFrame(summary, 100, 105, 100.95, false),
      late = settlementFrame(summary, 100, 105, 101.25, false);
    expect(early.stage).toBe('shopping');
    expect(late.stage).toBe('shopping');
    for (const seat of [0, 1]) {
      const a = early.from.players[seat].gold,
        b = early.to.players[seat].gold;
      expect(a).not.toBe(b);
      expect(interpolate(a, b, early.progress)).toBeGreaterThan(interpolate(a, b, late.progress));
      expect(interpolate(a, b, early.progress)).toBeLessThan(a);
    }
  });
  it('resumes at server time, never replays expired scenes, and keeps reduced-motion facts', () => {
    const p = presentationValue(wire.rounds[0].summary);
    expect(settlementFrame(p, 100, 105, 103, false).stage).toBe('score');
    const expired = settlementFrame(p, 100, 105, 120, false);
    expect(expired.to).toEqual(p.after);
    expect(expired.finished).toBe(true);
    const reduced = settlementFrame(p, 100, 105, 101, true);
    expect(reduced.from).toEqual(p.before);
    expect(reduced.to).toEqual(p.after);
    expect(reduced.progress).toBe(1);
  });
  it('retains round-start replenishment independently from settlement frames', () => {
    const transition = startEvents(wire.rounds[1].start)[0].transition!;
    expect(transition.after.players[0].burst).toBeGreaterThan(transition.before.players[0].burst);
    const mid = interpolate(
      transition.before.players[0].burst,
      transition.after.players[0].burst,
      0.2,
    );
    expect(mid).toBeGreaterThan(transition.before.players[0].burst);
    expect(mid).toBeLessThan(transition.after.players[0].burst);
  });
});
