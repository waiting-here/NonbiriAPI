import { describe, expect, it } from 'vitest';
import pinned from './motion_v3_golden.json';
import { contentHash, parseLevel, seedCommitForVersion, stateDigest, stateDigestForVersion } from './canonical';
import { Engine, replay } from './engine';
import { decodeHex, hex, sha256, utf8 } from './sha256';
import type { InputTuple, Level, ReplayResult } from './types';

interface Vector {
  name: string; level: Level; seed: string; inputs: InputTuple[] | null;
  trace_hash: string; tick_states: number; result: ReplayResult;
}
const vectors = pinned as unknown as Vector[];
describe('version 3 product engine traces', () => {
  for (const vector of vectors) {
    it(vector.name, () => {
      const level = parseLevel(JSON.stringify(vector.level));
      const hashes: string[] = [];
      const seed = decodeHex(vector.seed);
      const result = replay(level, seed, vector.inputs ?? [], { onTick: state => {
        hashes.push(stateDigestForVersion(3, state));
        if (vector.name === 'maximum-duration-continuous-turn') {
          const fish = state.fish[0];
          expect(Number.isSafeInteger(fish.motion!.turn_remainder)).toBe(true);
          expect([fish.x, fish.y]).toEqual([100 * 64, 100 * 64]);
        }
      } });
      expect(hashes).toHaveLength(vector.tick_states);
      expect(hex(sha256(utf8(hashes.join(String.fromCharCode(10)) + String.fromCharCode(10))))).toBe(vector.trace_hash);
      expect(result).toEqual(vector.result);
      const old = { ...level, engine_version: 2 as const };
      expect(contentHash(old)).not.toBe(result.content_hash);
      expect(seedCommitForVersion('challenge', 'period', 'node', result.content_hash, 3, 1, seed))
        .not.toBe(seedCommitForVersion('challenge', 'period', 'node', result.content_hash, 2, 1, seed));
      if (vector.name.startsWith('maximum-duration-')) {
        expect(result.terminal_tick).toBe(36_000);
        expect(result.reason).toBe('timeout');
      }
    }, 30_000);
  }
  it('retains v2 carry validation and rejects invalid v3 remainder', () => {
    const first = vectors[0];
    const engine = new Engine(first.level, decodeHex(first.seed));
    const state = engine.state();
    state.fish[0].motion!.turn_remainder = 5000;
    expect(() => stateDigest(state)).toThrow();
    expect(() => stateDigestForVersion(3, state)).not.toThrow();
    state.fish[0].motion!.turn_remainder = 5_000_000_000_000;
    expect(() => stateDigestForVersion(3, state)).toThrow();
    expect(engine.state().fish[0].motion!.turn_remainder).toBe(0);
  });
  it('recovers the input prefix and releases turn memory after tool return', () => {
    const vector = vectors.find(item => item.name === 'tool-memory-release-and-reencounter')!;
    const inputs = vector.inputs ?? [];
    const engine = new Engine(vector.level, decodeHex(vector.seed));
    for (let tick = 0; tick < 6; tick++) engine.step(inputs.filter(input => input[0] === tick));
    const checkpoint = engine.stateHash();
    const before = engine.state().fish[0];
    engine.step(inputs.filter(input => input[0] === 6));
    const after = engine.state().fish[0];
    expect(after.heading).toBe(before.heading);
    expect(after.rng).toEqual(before.rng);
    expect(after.motion).toEqual({ turn_remainder: 0, ambiguous_turn_dir: 0 });
    expect([after.x, after.y]).not.toEqual([before.x, before.y]);
    const result = replay(parseLevel(JSON.stringify(vector.level)), decodeHex(vector.seed), inputs, {
      onTick: state => { if (state.tick === 6) expect(stateDigestForVersion(3,state)).toBe(checkpoint); },
    });
    while (!engine.terminal) engine.step(inputs.filter(input => input[0] === engine.tick));
    expect(engine.result()).toEqual(result);
  });
});
