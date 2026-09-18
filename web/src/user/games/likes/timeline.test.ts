import { describe, expect, it } from 'vitest';
import { likesCodec, presentationValue } from './normalize';
import { settlementFrame, scoreMotion } from './motion';
import { eventTime, presentationSteps } from './timeline';
import wire from './testdata/authority.json';
import timings from './testdata/timelines.json';

describe('server-authored presentation timeline', () => {
  it('retains every consecutive cast at its own readable time, including after five seconds', () => {
    const timing = timings.scenarios.chain;
    const p = presentationValue({ ...wire.scenarios.chain.summary, timeline: timing.timeline });
    expect(likesCodec.presentationDuration!(p)).toBe(17);
    const seen: number[] = [];
    for (const step of presentationSteps(p, 100, 117)) {
      const frame = settlementFrame(
        p,
        100,
        117,
        100 + (step.offsetMS + step.durationMS / 2) / 1000,
        false,
      );
      expect(frame.finished).toBe(false);
      expect(frame.events.map((event) => event.id)).toEqual(step.eventIDs);
      for (const event of frame.events) {
        seen.push(event.id);
        if (event.cast) expect(step.durationMS).toBeGreaterThanOrEqual(2400);
        expect(eventTime(p, 100, 117, event)).toBe(100000 + step.offsetMS);
      }
    }
    expect(seen.sort((a, b) => a - b)).toEqual(
      p.events.map((event) => event.id).sort((a, b) => a - b),
    );
    expect(settlementFrame(p, 100, 117, 111, false).finished).toBe(false);
    expect(settlementFrame(p, 100, 117, 118, false).to).toEqual(p.after);
  });

  it('resumes at the current step and keeps the same full duration with reduced motion', () => {
    const p = presentationValue({
      ...wire.scenarios.chain.summary,
      timeline: timings.scenarios.chain.timeline,
    });
    const normal = settlementFrame(p, 100, 117, 111, false);
    const reduced = settlementFrame(p, 100, 117, 111, true);
    expect(normal.events.map((event) => event.id)).toEqual([9]);
    expect(reduced.events).toEqual(normal.events);
    expect(reduced.progress).toBe(1);
    expect(reduced.finished).toBe(false);
    expect(reduced.to).toEqual(p.after);
  });

  it('does not cap an otherwise valid longer event schedule', () => {
    const timeline = timings.scenarios.chain.timeline.map((step) => ({
      ...step,
      duration_ms: step.duration_ms * 4,
    }));
    const p = presentationValue({ ...wire.scenarios.chain.summary, timeline });
    expect(likesCodec.presentationDuration!(p)).toBe(68);
    expect(settlementFrame(p, 100, 168, 130, false).finished).toBe(false);
  });

  it('advances the score once per awarded cast instead of revealing all follow-ups early', () => {
    const p = presentationValue({
      ...wire.scenarios.chain.summary,
      timeline: timings.scenarios.chain.timeline,
    });
    let previous = p.before.players[0].likes;
    for (const step of presentationSteps(p, 100, 117).filter((step) =>
      p.events.some((event) => event.stage === step.stage && event.cast),
    )) {
      const frame = settlementFrame(p, 100, 117, 100 + step.offsetMS / 1000, false);
      const score = scoreMotion(frame, 0, false);
      expect(score.from).toBe(previous);
      expect(score.to - score.from).toBe(
        frame.events
          .filter((event) => event.seat === 0)
          .reduce((sum, event) => sum + (event.cast?.likes ?? 0), 0),
      );
      previous = score.to;
    }
    expect(previous).toBe(p.after.players[0].likes);
  });

  it('rejects omitted, duplicate, out-of-stage and reordered presentation references', () => {
    const source = { ...wire.scenarios.chain.summary, timeline: timings.scenarios.chain.timeline };
    const missing = structuredClone(source);
    missing.timeline[2].event_ids = [];
    expect(() => presentationValue(missing)).toThrow();
    const repeated = structuredClone(source);
    repeated.timeline[3].event_ids = [2];
    expect(() => presentationValue(repeated)).toThrow();
    const wrongStage = structuredClone(source);
    wrongStage.timeline[2].event_ids = [6];
    expect(() => presentationValue(wrongStage)).toThrow();
    const reversed = structuredClone(source);
    reversed.timeline.reverse();
    expect(() => presentationValue(reversed)).toThrow();
  });
});
