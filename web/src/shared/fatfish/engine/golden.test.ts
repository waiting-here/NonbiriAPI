import { describe, expect, it } from "vitest";
import pinned from "./golden.json";
import { parseLevel, seedCommit, stateDigest } from "./canonical";
import { Engine, replay } from "./engine";
import { decodeHex, hex, sha256, utf8 } from "./sha256";
import { initialFishRNG, nextTurnBit } from "./protocol";
import { SIN_TABLE, TRIG_SOURCE_SHA256 } from "./trig";
import { type InputTuple, type Level, type ReplayResult } from "./types";

interface GoldenCase {
  name: string;
  level: Level;
  seed: string;
  inputs: InputTuple[] | null;
  hashes: string[];
  result: ReplayResult;
}
const suite = pinned as unknown as { trig_sha256: string; seed_commit: string; turn_bits: string; cases: GoldenCase[] };

describe("shared deterministic replay vectors", () => {
  it("pins the single generated trigonometric source", () => {
    expect(TRIG_SOURCE_SHA256).toBe(suite.trig_sha256);
    expect(hex(sha256(utf8(`${SIN_TABLE.join("\n")}\n`)))).toBe(suite.trig_sha256);
  });
  it("binds the seed and reproduces 128 independent turn bits", () => {
    const first = suite.cases[0];
    const seed = decodeHex(first.seed);
    expect(seedCommit("challenge_1", "period_1", "node_1", first.result.content_hash, seed)).toBe(suite.seed_commit);
    const rng = initialFishRNG(seed, 1);
    let bits = "";
    for (let index = 0; index < 128; index++) bits += String(nextTurnBit(rng));
    expect(bits).toBe(suite.turn_bits);
  });
  for (const vector of suite.cases) {
    it(vector.name, () => {
      const level = parseLevel(JSON.stringify(vector.level));
      const seed = decodeHex(vector.seed);
      const initial = new Engine(level, seed);
      expect(stateDigest(initial.state())).toBe(vector.hashes[0]);
      const hashes: string[] = [];
      const result = replay(level, seed, vector.inputs ?? [], { onTick: (state) => hashes.push(stateDigest(state)) });
      expect(hashes).toEqual(vector.hashes);
      expect(result).toEqual(vector.result);
    });
  }
});
