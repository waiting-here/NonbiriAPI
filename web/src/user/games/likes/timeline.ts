import { STAGES } from './labels';
import type { LikesEvent, Presentation } from './types';

export function presentationSteps(p: Presentation, started: number, ends: number) {
  const steps =
    p.timeline ??
    STAGES.map((stage) => ({
      stage,
      durationMS: ((ends - started) * 1000) / STAGES.length,
      eventIDs: p.events.filter((event) => event.stage === stage).map((event) => event.id),
    }));
  let offsetMS = 0;
  return steps.map((step, index) => {
    const result = { ...step, index, offsetMS };
    offsetMS += step.durationMS;
    return result;
  });
}

export function eventTime(p: Presentation, started: number, ends: number, event: LikesEvent) {
  const step = presentationSteps(p, started, ends).find((step) => step.eventIDs.includes(event.id));
  return Math.round(started * 1000 + (step?.offsetMS ?? 0));
}

export function stageTime(p: Presentation, started: number, ends: number, stage: string) {
  const step = presentationSteps(p, started, ends).find((step) => step.stage === stage);
  return Math.round(started * 1000 + (step?.offsetMS ?? (ends - started) * 1000));
}
