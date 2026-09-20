import {
  booleanValue,
  enumValue,
  exactRecord,
  invalidResponse,
  safeInteger,
} from '../common/strict';
import { list, nullable, pair, seatValue } from '../common/duel/normalize';
import type { DuelCodec } from '../common/duel/types';
import { roleID } from './catalog';
import { STAGES } from './labels';
import { shortageValue } from './shortage';
import { amount, dictionary, label, prose, safeJSON, signedAmount, unique } from './value';
import type {
  Cast,
  Choice,
  Distill,
  EffectCue,
  Frame,
  LikesAction,
  LikesEvent,
  LikesView,
  Plan,
  Player,
  Presentation,
  Purchase,
  Resources,
  RoundFacts,
  Score,
  Selection,
  Status,
  Subscription,
  TurnRecord,
} from './types';

const optionalNumber = (r: Record<string, unknown>, key: string) =>
  r[key] === undefined ? undefined : amount(r[key]);
const optionalString = (r: Record<string, unknown>, key: string) =>
  r[key] === undefined ? undefined : label(r[key]);
const bool = (v: unknown) => booleanValue(v, 'flag');
const strings = (v: unknown) => unique(v, 48, label, (v) => v);
export function selectionValue(value: unknown): Selection {
  const r = exactRecord(value, ['role', 'harness', 'skills']);
  return {
    role: roleID(r.role),
    harness: nullable(r.harness, label),
    skills: unique(r.skills, 6, label, (v) => v),
  };
}
export function choiceValue(value: unknown): Choice {
  const r = exactRecord(value, ['skillId'], ['pay', 'cleanseMode', 'targets']);
  return {
    skillId: label(r.skillId),
    pay: r.pay === undefined ? undefined : enumValue(r.pay, ['auto', 'api'], 'payment'),
    cleanseMode:
      r.cleanseMode === undefined
        ? undefined
        : enumValue(r.cleanseMode, ['self', 'opponent'], 'cleanse mode'),
    targets: r.targets === undefined ? undefined : unique(r.targets, 128, label, (v) => v),
  };
}
export function planValue(value: unknown): Plan {
  const r = exactRecord(value, ['purchases', 'main', 'extra']);
  const purchases = list(r.purchases, 2, (v) => {
    const q = exactRecord(v, ['item'], ['target']);
    return {
      item: enumValue(q.item, ['sub', 'api', 'charge', 'cleanse', 'regulator'], 'purchase'),
      target: optionalString(q, 'target'),
    } satisfies Purchase;
  });
  return { purchases, main: nullable(r.main, choiceValue), extra: list(r.extra, 1, choiceValue) };
}
function status(value: unknown): Status {
  const r = exactRecord(
    value,
    ['key', 'kind', 'name', 'positive', 'p', 'q', 'remaining', 'layers', 'buffId', 'activeFrom'],
    ['category', 'targetSkill', 'expires', 'refreshedTurn', 'appliedBy', 'persistentLayers'],
  );
  return {
    key: label(r.key),
    kind: label(r.kind),
    name: label(r.name),
    positive: bool(r.positive),
    p: amount(r.p),
    q: amount(r.q),
    remaining: amount(r.remaining),
    layers: amount(r.layers),
    buffId: label(r.buffId),
    activeFrom: amount(r.activeFrom),
    category: optionalString(r, 'category'),
    targetSkill: optionalString(r, 'targetSkill'),
    expires: optionalNumber(r, 'expires'),
    refreshedTurn: optionalNumber(r, 'refreshedTurn'),
    appliedBy: optionalString(r, 'appliedBy'),
    persistentLayers: optionalNumber(r, 'persistentLayers'),
  };
}
function subscription(value: unknown): Subscription {
  const r = exactRecord(value, [
    'burstInitial',
    'totalInitial',
    'totalCap',
    'burstResetAt',
    'totalResetAt',
  ]);
  return {
    burstInitial: amount(r.burstInitial),
    totalInitial: amount(r.totalInitial),
    totalCap: amount(r.totalCap),
    burstResetAt: nullable(r.burstResetAt, amount),
    totalResetAt: nullable(r.totalResetAt, amount),
  };
}
function distill(value: unknown): Distill {
  const r = exactRecord(value, ['template', 'level', 'learning', 'usedSamples']);
  return {
    template: nullable(r.template, label),
    level: nullable(r.level, (v) => enumValue(v, ['I', 'II'], 'distill level')),
    learning: amount(r.learning),
    usedSamples: list(r.usedSamples, 512, amount),
  };
}
function player(value: unknown): Player {
  const r = exactRecord(
    value,
    [
      'role',
      'harness',
      'activeSlots',
      'gold',
      'likes',
      'burstCap',
      'burst',
      'sub',
      'api',
      'images',
      'apiPack',
      'resources',
      'resourceCaps',
      'subscription',
      'normalTurns',
      'stunned',
      'overloaded',
      'effects',
      'revealed',
      'slots',
      'fog',
      'used',
      'skillDecay',
      'distill',
    ],
    ['trial', 'loadout'],
  );
  const fog = bool(r.fog),
    slots = list(r.slots, 6, (v) => nullable(v, label));
  if (fog && r.loadout !== undefined) invalidResponse('concealed loadout');
  if (!fog && r.loadout === undefined) invalidResponse('own loadout');
  const loadout = r.loadout === undefined ? undefined : strings(r.loadout);
  const revealed = strings(r.revealed),
    used = dictionary(r.used, amount, 48),
    skillDecay = dictionary(r.skillDecay, amount, 96);
  const activeSlots = safeInteger(r.activeSlots, 4, 6, 'active slots');
  if (
    slots.length !== activeSlots ||
    (loadout && loadout.length > activeSlots) ||
    (fog && !revealed.includes('PUB41') && r.distill !== null) ||
    (fog &&
      Object.keys(skillDecay).some((key) =>
        key.endsWith(':distilled') ? !revealed.includes('PUB41') : !revealed.includes(key),
      ))
  )
    invalidResponse('concealed runtime state');
  if (
    fog &&
    (slots.some((id) => id !== null && !revealed.includes(id)) ||
      Object.keys(used).some((id) => !revealed.includes(id)))
  )
    invalidResponse('concealed skill');
  return {
    role: roleID(r.role),
    harness: nullable(r.harness, label),
    activeSlots,
    gold: amount(r.gold),
    likes: amount(r.likes),
    burstCap: amount(r.burstCap),
    burst: amount(r.burst),
    sub: amount(r.sub),
    api: amount(r.api),
    images: amount(r.images),
    apiPack: amount(r.apiPack),
    trial: optionalNumber(r, 'trial'),
    resources: dictionary(r.resources, amount, 16),
    resourceCaps: dictionary(r.resourceCaps, amount, 16),
    subscription: subscription(r.subscription),
    normalTurns: amount(r.normalTurns),
    stunned: bool(r.stunned),
    overloaded: bool(r.overloaded),
    effects: unique(r.effects, 128, status, (v) => v.key),
    revealed,
    slots,
    fog,
    loadout,
    used,
    skillDecay,
    distill: nullable(r.distill, distill),
  };
}
function record(value: unknown): TurnRecord {
  const r = exactRecord(
    value,
    ['skipped', 'stunned', 'last', 'lastMain', 'lastSuccess', 'lastCopyable'],
    ['nonbasicSuccess'],
  );
  const sample = (v: unknown) => {
    const q = exactRecord(v, ['id', 'skillId', 'success', 'kind', 'derived']);
    return {
      id: amount(q.id),
      skillId: label(q.skillId),
      success: bool(q.success),
      kind: label(q.kind),
      derived: bool(q.derived),
    };
  };
  return {
    skipped: bool(r.skipped),
    stunned: bool(r.stunned),
    last: nullable(r.last, sample),
    lastMain: nullable(r.lastMain, sample),
    lastSuccess: nullable(r.lastSuccess, sample),
    lastCopyable: nullable(r.lastCopyable, sample),
    nonbasicSuccess: r.nonbasicSuccess === undefined ? undefined : bool(r.nonbasicSuccess),
  };
}
export function likesView(value: unknown): LikesView {
  const r = exactRecord(value, ['energy', 'players', 'records', 'locked_plan']);
  return {
    energy: amount(r.energy),
    players: pair(r.players, player),
    records: pair(r.records, (v) => nullable(v, record)),
    lockedPlan: nullable(r.locked_plan, planValue),
  };
}
function effectCue(value: unknown): EffectCue {
  const r = exactRecord(value, ['key', 'kind', 'buff_id', 'layers', 'remaining', 'active_from']);
  return {
    key: label(r.key),
    kind: label(r.kind),
    buff_id: label(r.buff_id),
    layers: amount(r.layers),
    remaining: amount(r.remaining),
    active_from: amount(r.active_from),
  };
}
function resources(value: unknown, full: boolean): Resources {
  const r = exactRecord(value, [
    'gold',
    'likes',
    'burst',
    'burst_cap',
    'sub',
    'sub_cap',
    'api',
    'trial',
    'resources',
    'resource_caps',
    'effects',
    ...(full ? ['subscription'] : []),
  ]);
  if (full) subscription(r.subscription);
  return {
    gold: amount(r.gold),
    likes: amount(r.likes),
    burst: amount(r.burst),
    burst_cap: amount(r.burst_cap),
    sub: amount(r.sub),
    sub_cap: amount(r.sub_cap),
    api: amount(r.api),
    trial: amount(r.trial),
    resources: dictionary(r.resources, amount, 16),
    resource_caps: dictionary(r.resource_caps, amount, 16),
    effects: full
      ? list(r.effects, 128, status).map((s) => ({
          key: s.key,
          kind: s.kind,
          buff_id: s.buffId,
          layers: s.layers,
          remaining: s.remaining,
          active_from: s.activeFrom,
        }))
      : list(r.effects, 128, effectCue),
  };
}
function frame(value: unknown, full: boolean): Frame {
  const r = exactRecord(value, ['stage', 'players', 'energy', ...(full ? ['event_end'] : [])]);
  if (full) amount(r.event_end);
  return {
    stage: label(r.stage),
    players: pair(r.players, (v) => resources(v, full)),
    energy: amount(r.energy),
  };
}
function score(value: unknown): Score {
  const r = exactRecord(value, [
    'original',
    'parts',
    'intrinsic',
    'conditional',
    'before_multiplier',
    'multiplier',
    'passive',
    'final',
  ]);
  return {
    original: amount(r.original),
    parts: list(r.parts, 128, (v) => {
      const p = exactRecord(v, ['key', 'amount'], ['buff_id']);
      return {
        key: label(p.key),
        amount: signedAmount(p.amount),
        buff_id: optionalString(p, 'buff_id'),
      };
    }),
    intrinsic: signedAmount(r.intrinsic),
    conditional: signedAmount(r.conditional),
    before_multiplier: signedAmount(r.before_multiplier),
    multiplier: amount(r.multiplier),
    passive: signedAmount(r.passive),
    final: amount(r.final),
  };
}
function stepValue(value: unknown): NonNullable<Cast['step']> {
  const r = exactRecord(value, ['kind', 'index', 'likes']);
  const kind = enumValue(r.kind, ['main', 'extra', 'flash'], 'settlement step');
  return {
    kind,
    index: safeInteger(r.index, 0, kind === 'flash' ? 4 : 0, 'step index'),
    likes: pair(r.likes, amount),
  };
}
function applicationValue(value: unknown): NonNullable<Cast['applications']>[number] {
  const r = exactRecord(value, ['buff_id', 'target', 'success', 'resisted', 'derived']);
  const success = safeInteger(r.success, 0, 4, 'successful layers'),
    resisted = safeInteger(r.resisted, 0, 4, 'resisted layers');
  if (success + resisted < 1 || success + resisted > 4) invalidResponse('attempted layers');
  return {
    buffID: label(r.buff_id),
    target: seatValue(r.target),
    success,
    resisted,
    derived: bool(r.derived),
  };
}
function validateAttempt(value: unknown) {
  const r = exactRecord(value, [
    'rules_version',
    'step',
    'source',
    'target',
    'skill_id',
    'buff_id',
    'layer',
    'hit',
    'resist',
    'numerator',
    'denominator',
    'success',
    'draw',
    'derived',
  ]);
  safeInteger(r.rules_version, 2, 2, 'application rules');
  stepValue(r.step);
  if (seatValue(r.source) === seatValue(r.target)) invalidResponse('hostile effect target');
  label(r.skill_id);
  label(r.buff_id);
  safeInteger(r.layer, 1, 4, 'effect layer');
  const hit = safeInteger(r.hit, 0, 50, 'hit'),
    resist = safeInteger(r.resist, 0, 50, 'resistance');
  if (
    ![0, 25, 50].includes(hit) ||
    ![0, 25, 50].includes(resist) ||
    r.numerator !== 100 + hit ||
    r.denominator !== 100 + resist
  )
    invalidResponse('effect probability');
  const success = bool(r.success);
  bool(r.derived);
  if (hit >= resist ? r.draw !== null || !success : r.draw === null)
    invalidResponse('application draw');
  if (r.draw !== null) safeInteger(r.draw, 1, 10000, 'draw ordinal');
}
function cast(value: unknown): Cast {
  const r = exactRecord(
    value,
    [
      'skillId',
      'derived',
      'main',
      'energy',
      'token',
      'likes',
      'passiveLikes',
      'trialPayment',
      'subPayment',
      'apiPayment',
      'gold',
      'resourceCosts',
      'templateId',
      'success',
    ],
    ['level', 'step', 'characterPassive', 'applications'],
  );
  if (!bool(r.success)) invalidResponse('successful cast');
  return {
    skillId: label(r.skillId),
    derived: bool(r.derived),
    main: bool(r.main),
    energy: amount(r.energy),
    token: amount(r.token),
    likes: amount(r.likes),
    passiveLikes: amount(r.passiveLikes),
    trialPayment: amount(r.trialPayment),
    subPayment: amount(r.subPayment),
    apiPayment: amount(r.apiPayment),
    gold: amount(r.gold),
    resourceCosts: dictionary(r.resourceCosts, amount, 16),
    templateId: prose(r.templateId, 160),
    success: true,
    step: r.step === undefined ? undefined : stepValue(r.step),
    characterPassive:
      r.characterPassive === undefined
        ? undefined
        : enumValue(
            r.characterPassive,
            ['MULTIMODAL', 'SOTA_PRESSURE', 'WORLD_KNOWLEDGE', 'SECURITY_SHIELD', 'BLUE_FISH'],
            'character passive',
          ),
    applications:
      r.applications === undefined ? undefined : list(r.applications, 4, applicationValue),
    level:
      r.level === undefined
        ? null
        : nullable(r.level, (v) => enumValue(v, ['I', 'II'], 'cast level')),
  };
}
export function eventValue(value: unknown): LikesEvent {
  const r = exactRecord(value, ['id', 'round', 'stage', 'kind', 'seat'], ['data', 'score']);
  const kind = label(r.kind),
    data = r.data === undefined ? {} : dictionary(r.data, (v) => safeJSON(v), 128);
  if (data.shortage !== undefined) shortageValue(data.shortage);
  if (kind === 'effect-attempt') validateAttempt(data);
  let transition: LikesEvent['transition'] = null;
  if (kind === 'round-start') {
    const p = exactRecord(data, ['before', 'after']);
    transition = { before: frame(p.before, true), after: frame(p.after, true) };
  }
  return {
    id: amount(r.id),
    round: safeInteger(r.round, 1, 75, 'event round'),
    stage: label(r.stage),
    kind,
    seat: nullable(r.seat, seatValue),
    data,
    score: r.score === undefined ? null : score(r.score),
    cast: kind === 'cast' ? cast(r.data) : null,
    transition,
  };
}
export function presentationValue(value: unknown): Presentation {
  const r = exactRecord(value, ['plans', 'before', 'after', 'frames', 'events'], ['timeline']);
  const p: Presentation = {
    plans: pair(r.plans, planValue),
    before: frame(r.before, false),
    after: frame(r.after, false),
    frames: list(r.frames, 7, (v) => frame(v, false)),
    events: list(r.events, 512, eventValue),
  };
  if (r.timeline !== undefined) {
    const seen = new Set<number>();
    let lastStage = -1;
    p.timeline = list(r.timeline, 519, (value) => {
      const step = exactRecord(value, ['stage', 'duration_ms', 'event_ids']);
      const stage = enumValue(step.stage, STAGES, 'presentation stage');
      const index = STAGES.indexOf(stage);
      if (index < lastStage) invalidResponse('presentation order');
      lastStage = index;
      const seats = new Set<number | null>();
      const eventIDs = list(step.event_ids, 2, (value) => {
        const id = safeInteger(value, 1, Number.MAX_SAFE_INTEGER, 'presentation event');
        const event = p.events.find((event) => event.id === id);
        if (!event || seen.has(id) || event.stage !== stage || seats.has(event.seat))
          invalidResponse('presentation event reference');
        seen.add(id);
        seats.add(event.seat);
        return id;
      });
      if (seats.has(null) && eventIDs.length !== 1) invalidResponse('shared presentation event');
      return {
        stage,
        durationMS: safeInteger(step.duration_ms, 500, 60000, 'presentation step duration'),
        eventIDs,
      };
    });
    if (
      !p.timeline.length ||
      p.timeline[0].stage !== 'reveal' ||
      p.timeline.at(-1)?.stage !== 'round-end' ||
      seen.size !== p.events.length
    )
      invalidResponse('incomplete presentation');
    const duration = p.timeline.reduce((sum, step) => sum + step.durationMS, 0);
    if (duration % 1000 !== 0) invalidResponse('presentation clock');
  }
  return p;
}
export function roundFacts(value: unknown): RoundFacts {
  const r = exactRecord(value, [
    'round',
    'plans',
    'before',
    'after',
    'frames',
    'events',
    'draws',
    'result',
  ]);
  return {
    round: safeInteger(r.round, 1, 75, 'record round'),
    plans: pair(r.plans, planValue),
    before: frame(r.before, true),
    after: frame(r.after, true),
    frames: list(r.frames, 7, (v) => frame(v, true)),
    events: list(r.events, 512, eventValue),
    draws: list(r.draws, 512, (v) => {
      const d = exactRecord(v, ['ordinal', 'candidate_count', 'index']);
      const count = safeInteger(d.candidate_count, 1, 10000, 'draw candidates');
      return {
        ordinal: amount(d.ordinal),
        candidate_count: count,
        index: safeInteger(d.index, 0, count - 1, 'draw index'),
      };
    }),
    result: nullable(r.result, (v) => {
      const q = exactRecord(v, ['winner', 'reason', 'scores']);
      return {
        winner: nullable(q.winner, seatValue),
        reason: label(q.reason),
        scores: pair(q.scores, amount),
      };
    }),
  };
}
export const startEvents = (v: unknown) => list(v, 8, eventValue);
export function likesAction(value: unknown): LikesAction {
  const r = exactRecord(value, ['kind', 'plan']);
  return { kind: enumValue(r.kind, ['plan'], 'action'), plan: planValue(r.plan) };
}
export const likesCodec: DuelCodec<
  LikesView,
  RoundFacts,
  Presentation,
  LikesEvent[],
  Selection,
  LikesAction
> = {
  game: 'likes',
  modes: ['quick', 'standard'],
  view: likesView,
  facts: roundFacts,
  presentation: presentationValue,
  presentationDuration: (value) =>
    value.timeline ? value.timeline.reduce((total, step) => total + step.durationMS, 0) / 1000 : 5,
  start: startEvents,
  loadout: selectionValue,
  action: likesAction,
};
