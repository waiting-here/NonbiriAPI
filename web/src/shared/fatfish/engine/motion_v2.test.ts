import { describe, expect, it } from "vitest";
import pinned from "./motion_v2_golden.json";
import { canonicalJSON, parseLevel, seedCommit, seedCommitForVersion, stateDigest } from "./canonical";
import { Engine, replay } from "./engine";
import { polygonIntersectionArea } from "./geometry";
import { initialFishRNG, nextTurnWord } from "./protocol";
import { decodeHex } from "./sha256";
import { TRIG_SOURCE_SHA256 } from "./trig";
import { fishFootprint } from "./trig_helpers";
import type { InputTuple, Level, ReplayResult } from "./types";

interface GoldenCase {
  name: string;
  level: Level;
  seed: string;
  inputs: InputTuple[] | null;
  hashes: string[];
  result: ReplayResult;
}
const suite = pinned as unknown as { trig_sha256: string; seed_commit: string; turn_words: number[]; cases: GoldenCase[] };
function vector(name: string): GoldenCase {
  const result = suite.cases.find((item) => item.name === name);
  if (!result) throw new Error(`missing motion vector ${name}`);
  return result;
}
function inputsAt(item: GoldenCase, tick: number): InputTuple[] { return (item.inputs ?? []).filter((input) => input[0] === tick); }

describe("shared version 2 replay vectors", () => {
  it("keeps the trigonometric source, binds actual rules, and pins full random words", () => {
    const first = suite.cases[0], seed = decodeHex(first.seed);
    expect(suite.trig_sha256).toBe(TRIG_SOURCE_SHA256);
    expect(suite.cases).toHaveLength(18);
    expect(seedCommitForVersion("challenge_1", "period_1", "node_1", first.result.content_hash, 2, 1, seed)).toBe(suite.seed_commit);
    const legacy = seedCommit("challenge_1", "period_1", "node_1", first.result.content_hash, seed);
    expect(seedCommitForVersion("challenge_1", "period_1", "node_1", first.result.content_hash, 1, 1, seed)).toBe(legacy);
    expect(legacy).not.toBe(suite.seed_commit);
    const rng = initialFishRNG(seed, 1);
    expect(suite.turn_words.map(() => nextTurnWord(rng))).toEqual(suite.turn_words);
  });
  for (const item of suite.cases) {
    it(item.name, () => {
      const level = parseLevel(JSON.stringify(item.level)), seed = decodeHex(item.seed);
      const hashes: string[] = [];
      expect(level.engine_version).toBe(2);
      const result = replay(level, seed, item.inputs ?? [], { onTick: (state) => hashes.push(stateDigest(state)) });
      expect(hashes).toEqual(item.hashes);
      expect(result).toEqual(item.result);
    });
  }
});

describe("version 2 behavioral boundaries", () => {
  for (const [name, direction, words, ambiguous] of [
    ["front-probe-memory", 0, 3, true], ["right-probe-left-turn", -1, 2, false],
    ["left-probe-right-turn", 1, 2, false], ["both-side-probes-memory", 0, 3, true],
    ["thin-wall-canmove-fallback", 0, 3, true],
  ] as const) {
    it(`${name} turns without moving and consumes only its branch words`, () => {
      const item = vector(name), seed = decodeHex(item.seed), engine = new Engine(item.level, seed);
      const rng = initialFishRNG(seed, 1);
      for (let index = 0; index < words; index++) nextTurnWord(rng);
      engine.step();
      const fish = engine.state().fish[0];
      expect([fish.x, fish.y]).toEqual([100 * 64, 100 * 64]);
      expect(fish.rng).toEqual(rng);
      expect(fish.turn_distance).toBe(0);
      expect(fish.heading).not.toBe(0);
      if (direction !== 0) expect(fish.turn_dir).toBe(direction);
      expect(fish.motion?.ambiguous_turn_dir !== 0).toBe(ambiguous);
    });
  }
  it("moves at the existing speed without consuming random words on clear ground", () => {
    const item = vector("front-probe-memory"), level = parseLevel(JSON.stringify(item.level));
    level.solids = []; level.speed_pixels_per_second = 16;
    const seed = decodeHex(item.seed), engine = new Engine(level, seed);
    for (let tick = 0; tick < 60; tick++) engine.step();
    const fish = engine.state().fish[0];
    expect([fish.x, fish.y, fish.heading, fish.turn_dir, fish.turn_distance]).toEqual([116 * 64, 100 * 64, 0, 0, 0]);
    expect(fish.rng).toEqual(initialFishRNG(seed, 1));
    expect(fish.motion).toEqual({ turn_remainder: 0, ambiguous_turn_dir: 0 });
  });
  it("immediately releases turn memory and the remainder after tool return", () => {
    const item = vector("tool-memory-release-and-reencounter"), engine = new Engine(item.level, decodeHex(item.seed));
    for (let tick = 0; tick < 6; tick++) engine.step(inputsAt(item, tick));
    const before = engine.state().fish[0];
    engine.step(inputsAt(item, 6));
    const after = engine.state().fish[0];
    expect(after.heading).toBe(before.heading);
    expect(after.rng).toEqual(before.rng);
    expect([after.x, after.y]).not.toEqual([before.x, before.y]);
    expect(after.turn_dir).toBe(0);
    expect(after.motion).toEqual({ turn_remainder: 0, ambiguous_turn_dir: 0 });
  });
  it("gives legal partial escape priority over an obstructed front sensor", () => {
    const item = vector("partial-escape-overrides-front-probe"), engine = new Engine(item.level, decodeHex(item.seed));
    const before = engine.state().fish[0];
    engine.step();
    const after = engine.state().fish[0], cover = item.level.solids[0].polygon;
    expect(after.x).toBeGreaterThan(before.x);
    expect(after.heading).toBe(before.heading);
    expect(after.rng).toEqual(before.rng);
    const beforeArea = polygonIntersectionArea(fishFootprint(before.x, before.y), cover);
    expect(beforeArea.sign()).toBeGreaterThan(0);
    expect(polygonIntersectionArea(fishFootprint(after.x, after.y), cover).cmp(beforeArea)).toBeLessThan(0);
    expect(after.motion).toEqual({ turn_remainder: 0, ambiguous_turn_dir: 0 });
  });
  it("keeps full coverage immobile and resumes straight when the tool is returned", () => {
    const item = vector("full-cover-and-return"), engine = new Engine(item.level, decodeHex(item.seed));
    engine.step(inputsAt(item, 0));
    const before = engine.state().fish[0];
    expect([before.x, before.y]).toEqual([100 * 64, 100 * 64]);
    engine.step([[1, 2, "return", 2]]);
    const after = engine.state().fish[0];
    expect(after.heading).toBe(before.heading);
    expect(after.rng).toEqual(before.rng);
    expect([after.x, after.y]).not.toEqual([before.x, before.y]);
    expect(after.motion).toEqual({ turn_remainder: 0, ambiguous_turn_dir: 0 });
  });
  it("turns at all four floor edges and rotates the diagonal sensor with fixed-point truncation", () => {
    const floor = vector("four-floor-boundaries"), engine = new Engine(floor.level, decodeHex(floor.seed));
    const before = engine.state().fish;
    engine.step();
    engine.state().fish.forEach((fish, index) => {
      expect([fish.x, fish.y]).toEqual([before[index].x, before[index].y]);
      expect(fish.heading).not.toBe(before[index].heading);
    });
    const diagonal = vector("diagonal-fixed-point-probe"), rotated = new Engine(diagonal.level, decodeHex(diagonal.seed));
    rotated.step();
    expect(rotated.state().fish[0].heading).toBeLessThan(512);
  });
  it("omits v2 fields from v1 and rejects malformed or mixed motion states", () => {
    const item = vector("front-probe-memory"), seed = decodeHex(item.seed), level = parseLevel(JSON.stringify(item.level));
    level.engine_version = 1;
    expect(canonicalJSON(new Engine(level, seed).state())).not.toContain('"motion"');
    level.engine_version = 2;
    const engine = new Engine(level, seed), state = engine.state();
    expect(canonicalJSON(state)).toContain('"motion":{"ambiguous_turn_dir":0,"turn_remainder":0}');
    if (!state.fish[0].motion) throw new Error("missing v2 state");
    state.fish[0].motion.turn_remainder = 4321;
    expect(engine.state().fish[0].motion?.turn_remainder).toBe(0);
    for (const remainder of [-1, -0, 5000, 0.5, NaN]) {
      state.fish[0].motion.turn_remainder = remainder;
      expect(() => stateDigest(state)).toThrow("version 2 motion state is invalid");
    }
    state.fish[0].motion.turn_remainder = 0;
    const mixed = engine.state(), legacyFish = { ...mixed.fish[0] };
    delete legacyFish.motion;
    mixed.fish.push(legacyFish);
    expect(() => stateDigest(mixed)).toThrow("mixed motion state versions");
  });
  it("rejects unsupported rules in both level parsing and explicit commitments", () => {
    const item = suite.cases[0], seed = decodeHex(item.seed);
    for (const [engineVersion, scoringVersion] of [[0, 1], [4, 1], [2, 0], [2, 2]]) {
      expect(() => seedCommitForVersion("challenge", "period", "node", item.result.content_hash, engineVersion, scoringVersion, seed)).toThrow("unsupported");
      expect(() => parseLevel(JSON.stringify({ ...item.level, engine_version: engineVersion, scoring_version: scoringVersion }))).toThrow("unsupported");
    }
  });
  for (const version of [1, 2] as const) {
    it(`recovers the same saved input prefix under bound engine version ${version}`, () => {
      const item = vector("tool-memory-release-and-reencounter"), level = parseLevel(JSON.stringify(item.level));
      level.engine_version = version;
      const seed = decodeHex(item.seed), original = new Engine(level, seed);
      for (let tick = 0; tick < 5; tick++) original.step(inputsAt(item, tick));
      const checkpoint = original.stateHash(), recoveredLevel = parseLevel(JSON.stringify(level));
      const result = replay(recoveredLevel, seed, item.inputs ?? [], { onTick: (state) => {
        if (state.tick === 5) expect(stateDigest(state)).toBe(checkpoint);
      } });
      while (!original.terminal) original.step(inputsAt(item, original.tick));
      expect(result).toEqual(original.result());
      expect(result.engine_version).toBe(version);
      expect(result.scoring_version).toBe(1);
    });
  }
});
