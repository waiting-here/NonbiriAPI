import type { DuelHome, Resolution } from '../common/duel/types';
import { STAGES } from './labels';
import type { Cast, LikesEvent, LikesView, Presentation, Selection } from './types';
import { eventTime, stageTime } from './timeline';
import { settlementFrame } from './motion';
import { applicationVoices, followUpVoices, type EffectVoice } from '../common/audio/phrases';
import type { AudioFact } from '../common/audio/useArcadeAudio';

export type { AudioFact } from '../common/audio/useArcadeAudio';

export type LikesHome = DuelHome<LikesView, Presentation, LikesEvent[], Selection>;
export type MusicScene = 'lobby' | 'battle' | 'accelerated' | 'danger' | 'win' | 'draw' | 'loss';

function legacyStageTime(startedAt: number, endsAt: number, stage: string) {
  const index = STAGES.indexOf(stage as (typeof STAGES)[number]);
  const position = index < 0 ? 0 : index;
  return Math.round(
    (startedAt + (Math.max(0, endsAt - startedAt) * position) / STAGES.length) * 1000,
  );
}

function dataRecord(event: LikesEvent): Record<string, unknown> {
  return event.data as unknown as Record<string, unknown>;
}

function positiveNumber(value: unknown) {
  return typeof value === 'number' && Number.isFinite(value) && value > 0;
}

function statusRecord(event: LikesEvent): Record<string, unknown> | null {
  const value = dataRecord(event).status;
  return value !== null && typeof value === 'object' && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : null;
}

function paymentCue(cast: Cast) {
  return (
    positiveNumber(cast.token) ||
    positiveNumber(cast.trialPayment) ||
    positiveNumber(cast.subPayment) ||
    positiveNumber(cast.apiPayment) ||
    positiveNumber(cast.gold) ||
    Object.values(cast.resourceCosts).some(positiveNumber)
  );
}

function pushFact(
  facts: AudioFact[],
  seen: Set<string>,
  sessionID: string,
  round: number,
  eventID: string,
  cue: string,
  at: number,
  voices?: readonly EffectVoice[],
) {
  if (!Number.isFinite(at) || at < 0) return;
  const key = `likes:${sessionID}:${round}:${eventID}`;
  if (seen.has(`${key}:${cue}`)) return;
  seen.add(`${key}:${cue}`);
  facts.push({
    key,
    cue,
    at,
    ...(voices ? { playback: voices[0], accents: voices.slice(1) } : {}),
  });
}

function refillTransition(event: LikesEvent) {
  if (!event.transition) return false;
  return event.transition.after.players.some((after, seat) => {
    const before = event.transition!.before.players[seat];
    return (
      after.burst > before.burst ||
      after.sub > before.sub ||
      Object.keys(after.resources).some(
        (key) => (after.resources[key] ?? 0) > (before.resources[key] ?? 0),
      )
    );
  });
}

function effectCue(event: LikesEvent) {
  const status = statusRecord(event);
  if (!status) return null;
  const kind = typeof status.kind === 'string' ? status.kind.toUpperCase() : '';
  if (kind === 'STUN') return 'likes_stun';
  if (kind === 'COMBO') return null;
  return status.positive === true ? 'likes_buff' : null;
}

function eventFacts(
  facts: AudioFact[],
  seen: Set<string>,
  sessionID: string,
  event: LikesEvent,
  startedAt: number,
  endsAt: number,
  presentation?: Presentation,
  followups = new Map<string, number>(),
) {
  const at = presentation
    ? eventTime(presentation, startedAt, endsAt, event)
    : legacyStageTime(startedAt, endsAt, event.stage);
  const base = String(event.id);
  if (event.kind === 'cast' && event.cast) {
    const cast = event.cast;
    const counter = `${event.round}:${event.seat}`;
    const count = (followups.get(counter) ?? 0) + (cast.derived ? 1 : 0);
    if (cast.derived) followups.set(counter, count);
    let voices: readonly EffectVoice[] = cast.derived
      ? followUpVoices(count)
      : [
          {
            cue:
              cast.likes > 0 || (event.score?.final ?? 0) > 0 ? 'likes_score_burst' : 'likes_cast',
          },
        ];
    const applications = cast.applications?.filter((effect) => effect.target !== event.seat) ?? [];
    if (applications.length) {
      const success = applications.reduce((sum, effect) => sum + effect.success, 0);
      const resisted = applications.reduce((sum, effect) => sum + effect.resisted, 0);
      voices = [
        ...(cast.derived ? voices.slice(0, 1) : []),
        ...applicationVoices(success, resisted),
      ];
    }
    pushFact(facts, seen, sessionID, event.round, `${base}:impact`, voices[0].cue, at, voices);
    if (paymentCue(cast) && !cast.derived) {
      const paymentAt = presentation
        ? stageTime(presentation, startedAt, endsAt, 'payment')
        : legacyStageTime(startedAt, endsAt, 'payment');
      if (paymentAt !== at)
        pushFact(facts, seen, sessionID, event.round, `${base}:pay`, 'likes_pay', paymentAt);
    }
  }

  switch (event.kind) {
    case 'shop': {
      const item = dataRecord(event).item;
      const cue =
        item === 'charge'
          ? 'likes_charge'
          : item === 'sub'
            ? 'likes_subscription_upgrade'
            : 'likes_pay';
      pushFact(facts, seen, sessionID, event.round, `${base}:${cue}`, cue, at);
      break;
    }
    case 'charge':
      pushFact(facts, seen, sessionID, event.round, `${base}:charge`, 'likes_charge', at);
      break;
    case 'usage-reset':
      pushFact(
        facts,
        seen,
        sessionID,
        event.round,
        `${base}:refill`,
        'likes_subscription_refill',
        at,
      );
      break;
    case 'round-start':
      if (refillTransition(event))
        pushFact(
          facts,
          seen,
          sessionID,
          event.round,
          `${base}:refill`,
          'likes_subscription_refill',
          at,
        );
      break;
    case 'cleanse':
      pushFact(facts, seen, sessionID, event.round, `${base}:cleanse`, 'likes_cleanse', at);
      break;
    case 'overload':
      pushFact(facts, seen, sessionID, event.round, `${base}:overload`, 'likes_overload', at);
      break;
    case 'resource':
      pushFact(facts, seen, sessionID, event.round, `${base}:pay`, 'likes_pay', at);
      break;
    case 'effect': {
      const cue = effectCue(event);
      if (cue) pushFact(facts, seen, sessionID, event.round, `${base}:${cue}`, cue, at);
      break;
    }
    default:
      break;
  }
}

const negativeEffects = new Set([
  'STUN',
  'STOP',
  'SUBSCRIPTION_BAN',
  'SUPPRESS',
  'TOKEN_TAX',
  'NONBASIC_TAX',
  'OVERLOAD',
  'SOTA_FANATICISM',
  'BASE_SUPPRESS',
  'MODEL_DEGRADATION',
]);

function frameFacts(
  facts: AudioFact[],
  seen: Set<string>,
  sessionID: string,
  round: number,
  summary: Presentation,
  startedAt: number,
  endsAt: number,
) {
  let previous = summary.before.players.map(
    (player) => new Map(player.effects.map((effect) => [effect.key, effect.active_from])),
  );
  for (const frame of summary.frames) {
    frame.players.forEach((player, seat) => {
      for (const effect of player.effects) {
        if (previous[seat].get(effect.key) === effect.active_from) continue;
        const kind = effect.kind.toUpperCase();
        const cue =
          kind === 'STUN'
            ? 'likes_stun'
            : !negativeEffects.has(kind) && kind !== 'COMBO'
              ? 'likes_buff'
              : null;
        if (cue)
          pushFact(
            facts,
            seen,
            sessionID,
            round,
            `frame:${frame.stage}:${seat}:${effect.key}:${effect.active_from}`,
            cue,
            stageTime(summary, startedAt, endsAt, frame.stage),
          );
      }
    });
    previous = frame.players.map(
      (player) => new Map(player.effects.map((effect) => [effect.key, effect.active_from])),
    );
  }
}

function resolutionFacts(
  facts: AudioFact[],
  sessionID: string,
  resolution: {
    readonly round: number;
    readonly startedAt: number;
    readonly endsAt: number;
    readonly summary: Presentation;
  },
) {
  const seen = new Set<string>();
  const followups = new Map<string, number>();
  for (const event of resolution.summary.events)
    eventFacts(
      facts,
      seen,
      sessionID,
      event,
      resolution.startedAt,
      resolution.endsAt,
      resolution.summary,
      followups,
    );
  if (!resolution.summary.timeline)
    frameFacts(
      facts,
      seen,
      sessionID,
      resolution.round,
      resolution.summary,
      resolution.startedAt,
      resolution.endsAt,
    );
}

function eventListFacts(
  facts: AudioFact[],
  sessionID: string,
  events: readonly LikesEvent[],
  startedAt: number,
  endsAt: number,
) {
  const seen = new Set<string>();
  const followups = new Map<string, number>();
  for (const event of events)
    eventFacts(facts, seen, sessionID, event, startedAt, endsAt, undefined, followups);
}

function resultFact(home: LikesHome, facts: AudioFact[]) {
  if (home.current || !home.latestResult) return;
  const result = home.latestResult;
  const resolution = result.resolution;
  const cue =
    result.outcome === 'win'
      ? 'common_win'
      : result.outcome === 'draw'
        ? 'common_draw'
        : result.outcome === 'loss'
          ? 'likes_loss_stinger'
          : null;
  if (!cue) return;
  facts.push({
    key: `likes:${result.id}:${resolution?.round ?? 0}:result`,
    cue,
    at: Math.round((resolution?.endsAt ?? result.terminalAt) * 1000),
  });
}

function currentTransitionFacts(
  home: LikesHome,
  previous: LikesHome | undefined,
  facts: AudioFact[],
) {
  const current = home.current;
  if (!current || !previous) return;
  const at = Math.round(current.serverNow * 1000);
  if (!previous.current || previous.current.id !== current.id)
    facts.push({ key: `likes:${current.id}:match`, cue: 'common_match', at });
  if (previous.current?.id !== current.id) return;
  for (const seat of [0, 1] as const)
    if (!previous.current.locked[seat] && current.locked[seat])
      facts.push({
        key: `likes:${current.id}:${current.round}:lock:${seat}`,
        cue: 'common_lock',
        at,
      });
}

export function likesAudioFacts(home: LikesHome, previous?: LikesHome): AudioFact[] {
  const facts: AudioFact[] = [];
  currentTransitionFacts(home, previous, facts);
  if (home.current?.resolution) resolutionFacts(facts, home.current.id, home.current.resolution);
  if (home.current?.roundStart) {
    const start = home.current.roundStart;
    eventListFacts(facts, home.current.id, start.events, start.startedAt, start.startedAt + 2);
  }
  if (
    !home.current &&
    home.latestResult?.resolution &&
    home.latestResult.outcome !== 'system_cancelled'
  )
    resolutionFacts(facts, home.latestResult.id, home.latestResult.resolution);
  resultFact(home, facts);
  return facts;
}

function viewScene(view: LikesView | null, seat: 0 | 1, round: number): MusicScene {
  if (!view) return 'lobby';
  const player = view.players[seat];
  if (player.overloaded) return 'danger';
  if (player.effects.some((effect) => effect.kind === 'SPEED_MODE' && effect.activeFrom <= round))
    return 'accelerated';
  return 'battle';
}

function seconds(value: number, fallback: number) {
  if (!Number.isFinite(value)) return fallback;
  return value > 100_000_000_000 ? value / 1000 : value;
}

function presentedScene(
  view: LikesView | null,
  seat: 0 | 1,
  round: number,
  resolution: Resolution<Presentation> | null,
  now: number,
): MusicScene {
  if (resolution && now < resolution.endsAt) {
    const motion = settlementFrame(
      resolution.summary,
      resolution.startedAt,
      resolution.endsAt,
      now,
      false,
    );
    const effects = (motion.progress >= 0.35 ? motion.to : motion.from).players[seat].effects;
    const carriedOverload =
      effects.some((effect) => effect.kind === 'OVERLOAD' && effect.active_from <= round) &&
      resolution.summary.before.players[seat].effects.some(
        (effect) => effect.kind === 'OVERLOAD' && effect.active_from <= round,
      );
    if (
      carriedOverload ||
      motion.revealedEvents.some(
        (event) =>
          event.kind === 'overload' &&
          (event.seat === seat ||
            (event.seat === null &&
              Array.isArray(event.data.overloaded) &&
              event.data.overloaded[seat] === true)) &&
          now * 1000 >=
            eventTime(resolution.summary, resolution.startedAt, resolution.endsAt, event),
      )
    )
      return 'danger';
    if (effects.some((effect) => effect.kind === 'SPEED_MODE' && effect.active_from <= round))
      return 'accelerated';
    return 'battle';
  }
  return viewScene(view, seat, round);
}

export function likesMusicScene(home: LikesHome, now: number = home.serverNow): MusicScene {
  if (home.queue) return 'lobby';
  const nowSeconds = seconds(now, home.serverNow);
  if (home.current)
    return presentedScene(
      home.current.view,
      home.current.you,
      home.current.round,
      home.current.resolution,
      nowSeconds,
    );
  const result = home.latestResult;
  if (!result) return 'lobby';
  if (result.outcome === 'system_cancelled') return 'lobby';
  if (result.resolution && nowSeconds < result.resolution.endsAt)
    return presentedScene(
      result.view,
      result.you,
      result.resolution.round,
      result.resolution,
      nowSeconds,
    );
  if (!result.resolution && nowSeconds < result.terminalAt) return 'lobby';
  if (result.outcome === 'win') return 'win';
  if (result.outcome === 'draw') return 'draw';
  if (result.outcome === 'loss') return 'loss';
  return 'lobby';
}
