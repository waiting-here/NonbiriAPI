import { useEffect, useState, useSyncExternalStore } from 'react';
import { STAGES } from './labels';
import type { Frame, LikesEvent, LikesView, Presentation, Resources } from './types';
import type { Resolution, RoundStart } from '../common/duel/types';

function subscribeMotion(callback: () => void) {
  const query = matchMedia('(prefers-reduced-motion: reduce)');
  query.addEventListener('change', callback);
  return () => query.removeEventListener('change', callback);
}
export function useReducedMotion() {
  return useSyncExternalStore(
    subscribeMotion,
    () => matchMedia('(prefers-reduced-motion: reduce)').matches,
    () => true,
  );
}
export function useServerClock(serverNow: number, reduced: boolean, active = true) {
  const [time, setTime] = useState(serverNow);
  useEffect(() => {
    if (!active) return;
    const received = performance.now();
    const interval = setInterval(
      () =>
        setTime((previous) =>
          Math.max(previous, serverNow + (performance.now() - received) / 1000),
        ),
      reduced ? 150 : 33,
    );
    return () => clearInterval(interval);
  }, [serverNow, reduced, active]);
  return Math.max(time, serverNow);
}
export const interpolate = (before: number, after: number, progress: number) =>
  Math.round(before + (after - before) * (1 - Math.pow(1 - Math.max(0, Math.min(1, progress)), 3)));
export function overloadCues(p: Presentation, stage: string, reduced: boolean): [boolean, boolean] {
  const cues: [boolean, boolean] = [false, false];
  for (const event of p.events) {
    if (
      event.kind !== 'overload' ||
      (!reduced &&
        STAGES.indexOf(event.stage as (typeof STAGES)[number]) >
          STAGES.indexOf(stage as (typeof STAGES)[number]))
    )
      continue;
    if (event.seat !== null) cues[event.seat] = true;
    else if (Array.isArray(event.data.overloaded)) {
      cues[0] ||= event.data.overloaded[0] === true;
      cues[1] ||= event.data.overloaded[1] === true;
    }
  }
  return cues;
}
export function viewFrame(view: LikesView): Frame {
  return {
    stage: 'current',
    energy: view.energy,
    players: view.players.map((p) => ({
      gold: p.gold,
      likes: p.likes,
      burst: p.burst,
      burst_cap: p.burstCap,
      sub: p.sub,
      sub_cap: p.subscription.totalCap,
      api: p.api,
      trial: p.trial ?? 0,
      resources: p.resources,
      resource_caps: p.resourceCaps,
      effects: p.effects.map((s) => ({
        key: s.key,
        kind: s.kind,
        buff_id: s.buffId,
        layers: s.layers,
        remaining: s.remaining,
        active_from: s.activeFrom,
      })),
    })) as [Resources, Resources],
  };
}
export function settlementFrame(
  p: Presentation,
  started: number,
  ends: number,
  now: number,
  reduced: boolean,
) {
  const progress = Math.max(0, Math.min(1, (now - started) / (ends - started)));
  const index = Math.min(STAGES.length - 1, Math.floor(progress * STAGES.length));
  const stage = STAGES[index];
  if (reduced) return { stage, from: p.before, to: p.after, progress: 1, finished: now >= ends };
  const at = (last: number) => {
    let frame = p.before;
    for (let i = 0; i <= last; i++) frame = p.frames.find((f) => f.stage === STAGES[i]) ?? frame;
    return frame;
  };
  return {
    stage,
    from: at(index - 1),
    to: now >= ends ? p.after : at(index),
    progress: progress >= 1 ? 1 : progress * STAGES.length - index,
    finished: now >= ends,
  };
}

export function arenaMotion(
  view: LikesView,
  resolution: Resolution<Presentation> | null,
  roundStart: RoundStart<LikesEvent[]> | null,
  now: number,
  reduced: boolean,
) {
  const running = resolution !== null && now < resolution.endsAt;
  const transition = roundStart?.events.find((event) => event.transition)?.transition;
  const starting =
    !running && roundStart !== null && now < roundStart.startedAt + 2 && !!transition;
  const current = viewFrame(view);
  const motion = running
    ? settlementFrame(resolution.summary, resolution.startedAt, resolution.endsAt, now, reduced)
    : starting
      ? {
          from: transition.before,
          to: transition.after,
          stage: 'round-start',
          progress: reduced ? 1 : Math.max(0, (now - roundStart.startedAt) / 2),
        }
      : { from: current, to: current, stage: 'current', progress: 1 };
  return { running, starting, motion };
}
