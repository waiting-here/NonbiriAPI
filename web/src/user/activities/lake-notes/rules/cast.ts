import {
  addAmount,
  barHeight,
  bait,
  catalog,
  clamp,
  equipmentEffects,
  fish,
  fishAvailable,
  gear,
  levelFromXp,
  periodAt,
  round,
  RULES_ID,
  TICK_SECONDS,
  weatherForDay,
} from './catalog';
import {
  advanceClock,
  advanceContracts,
  awardBait,
  caught,
  catchValue,
  storeCatch,
  validateProfile,
} from './profile';
import { validateCast } from './state';
import type {
  Cast,
  Challenge,
  EncounterPlan,
  FishState,
  FishType,
  Loadout,
  MotionRandom,
  Profile,
  Snapshot,
} from './types';

export type Random53 = () => number;
const sample = (r: Random53) => {
  const n = r();
  if (!Number.isFinite(n) || n < 0 || n >= 1 || !Number.isInteger(n * 2 ** 53))
    throw Error('invalid random sample');
  return n;
};
const random = (r: Random53, a: number, b: number) => a + sample(r) * (b - a);
export function motionNext(r: MotionRandom) {
  r.state ^= r.state << 13;
  r.state ^= r.state >>> 17;
  r.state ^= r.state << 5;
  r.state >>>= 0;
  r.draw_count++;
  return r.state / 4294967296;
}
const motionRange = (r: MotionRandom, a: number, b: number) => a + motionNext(r) * (b - a);
export function makeChallenge(type: FishType, length: number): Challenge {
  const relativeSize = clamp((length - type.length[0]) / (type.length[1] - type.length[0]), 0, 1);
  return {
    difficulty: clamp(type.difficulty + round(relativeSize * 8), 5, 110),
    fishSpeed: 1,
    tempo: 1,
    gain: 1,
    loss: 1,
  };
}
export function initialFish(challenge: Challenge): FishState {
  const position = clamp((508 / 568) * 568, 0, 532);
  return {
    position,
    y: position / 568,
    speed: 0,
    target: clamp(((100 - challenge.difficulty) / 100) * 548, 0, 548),
    drift: 0,
  };
}
export function updateFish(f: FishState, r: MotionRandom, behavior: string, challenge: Challenge) {
  const steps = TICK_SECONDS * 60;
  const difficulty = challenge.difficulty;
  const tempo = challenge.tempo;
  if (
    motionNext(r) < ((difficulty * (behavior === 'smooth' ? 20 : 1)) / 4000) * tempo * steps &&
    (behavior !== 'smooth' || f.target < 0)
  ) {
    const percent = Math.min(0.99, (difficulty + motionRange(r, 10, 45)) / 100);
    f.target = clamp(f.position + motionRange(r, -f.position, 548 - f.position) * percent, 0, 548);
  }
  if (behavior === 'floater') f.drift = Math.max(-1.5, f.drift - 0.01 * steps);
  if (behavior === 'sinker') f.drift = Math.min(1.5, f.drift + 0.01 * steps);
  if (f.target >= 0 && Math.abs(f.position - f.target) > 3) {
    const acceleration =
      (f.target - f.position) / (motionRange(r, 10, 30) + 100 - Math.min(100, difficulty));
    f.speed += ((acceleration - f.speed) / 5) * steps;
  } else if (behavior !== 'smooth' && motionNext(r) < (difficulty / 2000) * tempo * steps) {
    f.target = clamp(f.position + (motionNext(r) < 0.5 ? -1 : 1) * motionRange(r, 50, 101), 0, 548);
  } else f.target = -1;
  if (behavior === 'dart' && motionNext(r) < (difficulty / 1000) * tempo * steps)
    f.target = clamp(
      f.position + (motionNext(r) < 0.5 ? -1 : 1) * motionRange(r, 51, 101 + difficulty * 2),
      0,
      548,
    );
  f.position = clamp(f.position + (f.speed + f.drift) * challenge.fishSpeed * steps, 0, 532);
  f.y = f.position / 568;
}
export function biteWindow(p: Profile, l: Loadout, baitId?: string) {
  const effects = equipmentEffects(l);
  const rod = gear(l.rod);
  if (!rod || !('baitAllowed' in rod) || !rod.baitAllowed) baitId = undefined;
  const maxGameSeconds = Math.max(
    0.6,
    30 - (levelFromXp(p.xp) / 2) * 0.25 - effects.spinnerSeconds,
  );
  const scale =
    0.75 * ((bait(baitId)?.effects as { biteDelay?: number } | undefined)?.biteDelay || 1) * 0.25;
  return { min: 0.6 * scale, max: maxGameSeconds * scale };
}
function pickFish(p: Profile, s: Snapshot, r: Random53) {
  const localFish = catalog.FISH_TYPES.filter(
    (f) =>
      f.location === p.location && fishAvailable(f, periodAt(p.clockMinutes), weatherForDay(p.day)),
  );
  const choices = s.rod === 'trainingRod' ? localFish.filter((f) => f.difficulty < 50) : localFish;
  const effects = bait(s.bait)?.effects as { rareWeight?: number; epicWeight?: number } | undefined;
  const weighted = choices.map((f) => {
    const rank = catalog.RARITIES[f.rarity as keyof typeof catalog.RARITIES].rank;
    let weight = f.weight;
    if (p.first === 'tracker' && rank >= 3) weight *= 1.2;
    if (p.second === 'legendHunter' && rank >= 4) weight *= 1.25;
    if (p.fourth === 'deepSeeker' && rank >= 4) weight *= 1.2;
    if (rank >= 3) weight *= Math.min(1.2, s.effects.rareWeight || 1) * (effects?.rareWeight || 1);
    if (rank >= 4) weight *= effects?.epicWeight || 1;
    if (rank === 5) weight *= s.effects.legendWeight || 1;
    return { fish: f, weight: Math.min(weight, f.weight * 4.5) };
  });
  let roll = random(
    r,
    0,
    weighted.reduce((sum, entry) => sum + entry.weight, 0),
  );
  for (const entry of weighted) {
    roll -= entry.weight;
    if (roll < 0) return entry.fish;
  }
  return choices[choices.length - 1];
}
/** Server-side sampling mirror for conformance. Browser gameplay consumes the server's Cast plan. */
export function start(p: Profile, r: Random53, seed: number): { profile: Profile; cast: Cast } {
  validateProfile(p);
  if (!Number.isInteger(seed) || seed < 1 || seed > 4294967295) throw Error('invalid seed');
  const profile = structuredClone(p);
  const rod = gear(p.equipped.rod);
  let baitId: string | undefined;
  if (
    rod &&
    'baitAllowed' in rod &&
    rod.baitAllowed &&
    p.selectedBait &&
    p.baitStock[p.selectedBait] > 0
  ) {
    baitId = p.selectedBait;
    profile.baitStock[baitId]--;
    if (!profile.baitStock[baitId]) delete profile.selectedBait;
  }
  const effects = equipmentEffects(p.equipped);
  const snapshot: Snapshot = {
    first: p.first,
    second: p.second,
    third: p.third,
    fourth: p.fourth,
    level: levelFromXp(p.xp),
    hadCaught: caught(p) > 0n,
    rod: p.equipped.rod,
    bait: baitId,
    location: p.location,
    effects,
    barHeight: barHeight(p, effects, p.equipped.rod, baitId),
  };
  const wait = biteWindow(p, p.equipped, baitId);
  const waitSeconds = random(r, wait.min, wait.max);
  const preview = structuredClone(p);
  let remaining = waitSeconds,
    biteTick = 0;
  while (remaining > 0) {
    biteTick++;
    advanceClock(preview, TICK_SECONDS * 3);
    remaining -= TICK_SECONDS;
  }
  const plan: EncounterPlan = {
    waitSeconds,
    biteTick,
    biteDay: preview.day,
    biteClockMinutes: preview.clockMinutes,
    length: 0,
    sizeFactor: 0,
    challenge: { difficulty: 0, fishSpeed: 0, tempo: 0, gain: 0, loss: 0 },
  };
  if (snapshot.hadCaught && sample(r) < 0.08)
    plan.debris = Object.keys(catalog.DEBRIS)[Math.floor(random(r, 0, 4))];
  else {
    const f = pickFish(preview, snapshot, r);
    const size =
      snapshot.rod === 'trainingRod'
        ? 0
        : clamp(random(r, 0.08, 0.68) + (snapshot.level / 20) * 0.25, 0, 1);
    plan.fishKind = f.kind;
    plan.sizeFactor = size;
    plan.length = round(f.length[0] + (f.length[1] - f.length[0]) * size);
    plan.challenge = makeChallenge(f, plan.length);
    if (p.third === 'reader') plan.challenge.tempo *= 0.92;
    const effects = bait(baitId)?.effects as
      { fishSpeed?: number; progressLoss?: number } | undefined;
    if (baitId) {
      plan.challenge.fishSpeed *= effects?.fishSpeed || 1;
      plan.challenge.loss *= effects?.progressLoss || 1;
    }
    if (snapshot.hadCaught && sample(r) < 0.15) plan.treasureY = random(r, 0.15, 0.85);
  }
  const cast: Cast = {
    rules_id: RULES_ID,
    snapshot,
    plan,
    phase: 'waiting',
    paused: false,
    tick: 0,
    held: false,
    waitRemaining: waitSeconds,
    barY: 0.65,
    barVelocity: 0,
    progress: 0.3,
    wasHit: true,
    elapsed: 0,
    hitTime: 0,
    effectiveTime: 0,
    currentMissTime: 0,
    longestMissTime: 0,
    motion: { state: seed, draw_count: 0 },
  };
  return { profile, cast };
}
export const terminal = (c: Cast) => c.phase === 'success' || c.phase === 'failed';
export function advance(p: Profile, c: Cast, held: boolean[]) {
  validateProfile(p);
  validateCast(c);
  if (held.length < 1 || held.length > 120 || c.paused || c.rules_id !== RULES_ID)
    throw Error('invalid simulation segment');
  const profile = structuredClone(p),
    cast = structuredClone(c);
  for (const h of held) {
    if (terminal(cast)) break;
    step(profile, cast, h);
  }
  return { profile, cast };
}
/** Mutates one tick of already validated server state; validate once at prediction/restoration entry. */
export function step(p: Profile, c: Cast, held: boolean) {
  if (terminal(c) || c.paused) return;
  c.tick++;
  c.held = held;
  advanceClock(p, TICK_SECONDS * 3);
  if (c.phase === 'waiting') {
    c.waitRemaining -= TICK_SECONDS;
    if (c.waitRemaining <= 0) {
      if (
        c.tick !== c.plan.biteTick ||
        p.day !== c.plan.biteDay ||
        p.clockMinutes !== c.plan.biteClockMinutes
      )
        throw Error('invalid encounter clock');
      if (c.plan.debris) {
        if (p.debrisStock[c.plan.debris] < 9999) p.debrisStock[c.plan.debris]++;
        else
          p.coins = addAmount(
            p.coins,
            catalog.DEBRIS[c.plan.debris as keyof typeof catalog.DEBRIS].value,
          );
        p.trashRecovered = addAmount(p.trashRecovered, 1);
        advanceContracts(p, 'cleanup');
        c.phase = 'success';
        c.progress = 0;
        c.held = false;
        c.result = {
          success: true,
          perfect: false,
          quality: 0,
          xp: 0,
          overflow: false,
          catchValue: 0,
          debris: c.plan.debris,
        };
      } else {
        c.phase = 'playing';
        c.barY = 1 - c.snapshot.barHeight / 2;
        c.barVelocity = 0;
        c.fish = initialFish(c.plan.challenge);
        if (c.plan.treasureY !== undefined)
          c.treasure = { y: c.plan.treasureY, progress: 0, secured: false };
      }
    }
    return;
  }
  if (c.phase !== 'playing' || !c.fish) throw Error('invalid simulation phase');
  const dt = TICK_SECONDS,
    s = c.snapshot,
    e = s.effects,
    challenge = c.plan.challenge,
    f = fish(c.plan.fishKind!)!;
  c.elapsed += dt;
  const controlBoost = (s.second === 'control' ? 1.08 : 1) * e.acceleration;
  const acceleration =
    ((0.25 * 60 * 60) / 568 + (held ? (-0.5 * 60 * 60) / 568 : 0)) *
    controlBoost *
    (c.wasHit ? (e.barbedCount ? 0.3 : 0.6) : 1);
  const halfBar = s.barHeight / 2;
  if (held && (c.barY <= halfBar || c.barY >= 1 - halfBar)) c.barVelocity = 0;
  if (c.wasHit && e.barbedCount)
    c.barVelocity += (Math.sign(c.fish.y - c.barY) * e.barbedCount * 0.2 * 60) / 568;
  c.barVelocity = clamp(c.barVelocity + acceleration * dt, -2.4, 2.4);
  c.barY += c.barVelocity * dt;
  if (c.barY < halfBar) {
    c.barY = halfBar;
    c.barVelocity = -c.barVelocity * (2 / 3);
  }
  if (c.barY > 1 - halfBar) {
    c.barY = 1 - halfBar;
    c.barVelocity = -c.barVelocity * e.bottomBounce;
  }
  updateFish(c.fish, c.motion, f.behavior, challenge);
  const hit = c.fish.y >= c.barY - halfBar && c.fish.y <= c.barY + halfBar;
  c.wasHit = hit;
  c.effectiveTime += dt;
  if (hit) {
    c.hitTime += dt;
    c.currentMissTime = 0;
  } else {
    c.currentMissTime += dt;
    c.longestMissTime = Math.max(c.longestMissTime, c.currentMissTime);
  }
  if (c.treasure && !c.treasure.secured && c.elapsed >= 2.2) {
    const covered = Math.abs(c.treasure.y - c.barY) <= halfBar;
    c.treasure.progress = clamp(c.treasure.progress + dt * (covered ? 1 / 2 : -0.25), 0, 1);
    if (c.treasure.progress >= 1 - 1e-9) {
      c.treasure.progress = 1;
      c.treasure.secured = true;
    }
  }
  const rank = catalog.RARITIES[f.rarity as keyof typeof catalog.RARITIES].rank;
  const rareBonus =
    (s.second === 'brawler' && rank >= 3 ? 1.08 : 1) *
    (s.fourth === 'deepSeeker' && rank >= 3 ? 1.05 : 1);
  const calmBonus = (s.second === 'calm' ? 0.9 : 1) * (s.third === 'patience' ? 0.94 : 1);
  const progressRate = hit
    ? 0.12 * challenge.gain * rareBonus * e.progressGain
    : s.hadCaught
      ? -0.18 * challenge.loss * calmBonus * e.progressLoss
      : 0;
  c.progress = clamp(c.progress + progressRate * dt, 0, 1);
  if (c.progress >= 1) finish(p, c, true);
  else if (c.progress <= 0) finish(p, c, false);
}
function finish(p: Profile, c: Cast, success: boolean) {
  c.phase = success ? 'success' : 'failed';
  c.held = false;
  p.streak = success ? addAmount(p.streak, 1) : '0';
  const f = fish(c.plan.fishKind!)!;
  const perfect =
    success &&
    c.effectiveTime > 0 &&
    c.hitTime / c.effectiveTime >= 0.85 &&
    c.longestMissTime <= 0.6;
  const baseQuality = c.plan.sizeFactor < 0.33 ? 0 : c.plan.sizeFactor < 0.66 ? 1 : 2;
  const tackleQuality = clamp(baseQuality + c.snapshot.effects.qualityBonus, 0, 3);
  const quality = perfect && tackleQuality > 0 ? Math.min(3, tackleQuality + 1) : tackleQuality;
  let amount = 0;
  const overflow = success && p.basket.length >= 80;
  const value = success
    ? catchValue({ kind: f.kind, quality } as Parameters<typeof catchValue>[0])
    : 0;
  if (success) {
    amount = Math.floor((baseQuality + 1) * 3 + c.plan.challenge.difficulty / 3);
    if (perfect) amount = Math.floor(amount * 2.4);
    if (f.rarity === 'legendary') amount *= 5;
    if (c.snapshot.first === 'tracker') amount = Math.floor(amount * 1.08);
    p.xp = addAmount(p.xp, amount);
    storeCatch(p, f, c.plan.length, quality, perfect);
  }
  const halfBar = barHeight(p, c.snapshot.effects, c.snapshot.rod, c.snapshot.bait) / 2;
  c.barY = clamp(c.barY, halfBar, 1 - halfBar);
  c.result = { success, perfect, quality, xp: amount, overflow, catchValue: value };
}
/** Server-private reward conformance; prediction never calls this to display a reward. */
export function resolveTreasure(p: Profile, c: Cast, r: Random53) {
  const profile = structuredClone(p),
    cast = structuredClone(c);
  if (cast.reward) return { profile, cast };
  if (cast.phase !== 'success' || !cast.treasure?.secured) throw Error('treasure is not earned');
  const coins = Math.floor(random(r, 25, 81)) + levelFromXp(profile.xp) * 2;
  const roll = sample(r),
    baitId = roll < 0.15 ? 'glimmer' : roll < 0.25 ? 'deluxe' : 'basic';
  const count = baitId === 'basic' ? Math.floor(random(r, 2, 5)) : 1;
  profile.coins = addAmount(profile.coins, coins);
  awardBait(profile, baitId, count);
  profile.treasureOpened = addAmount(profile.treasureOpened, 1);
  cast.reward = { coins, bait: baitId, count };
  return { profile, cast };
}
