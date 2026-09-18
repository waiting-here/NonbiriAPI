import raw from './authority.json' with { type: 'json' };
import { eventValue, likesView, planValue, presentationValue, selectionValue } from '../normalize';

export const tutorialData = {
  contentHash: raw.content_hash,
  selection: selectionValue(raw.selection),
  rounds: raw.rounds.map((r) => ({
    before: likesView(r.before),
    after: likesView(r.after),
    plan: planValue(r.plans[0]),
    summary: presentationValue(r.summary),
    seconds: r.seconds,
    start: r.start.map(eventValue),
  })),
};
