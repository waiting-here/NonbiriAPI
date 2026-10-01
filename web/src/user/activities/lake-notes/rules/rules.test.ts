import { createHash } from 'node:crypto';
import { describe, expect, it } from 'vitest';
import games from './testdata/full-game.json';
import actions from './testdata/actions.json';
import {
  advance,
  advanceClock,
  advanceContracts,
  applyAction,
  catalog,
  ensureContractBoard,
  fish,
  initialProfile,
  parseAmount,
  resolveTreasure,
  start,
  stateBytes,
  step,
  storeCatch,
  terminal,
  validateCast,
  validateProfile,
} from './index';
import type { Action, Cast, Profile } from './types';

describe('Lake Notes source conformance', () => {
  it('matches every original full-game tick and terminal profile', () => {
    expect(games.vectors).toHaveLength(279);
    for (const vector of games.vectors) {
      let at = 0;
      let { profile, cast } = start(
        vector.profile as Profile,
        () => vector.samples[at++],
        vector.seed,
      );
      expect(cast.plan).toEqual(vector.plan);
      const hash = createHash('sha256');
      for (const held of vector.held) {
        step(profile, cast, held);
        if (cast.phase === 'success' && cast.treasure?.secured) {
          let i = 0;
          ({ profile, cast } = resolveTreasure(profile, cast, () => vector.rewardSamples[i++]));
        }
        hash.update(stateBytes(profile, cast));
      }
      expect(hash.digest('hex'), vector.name).toBe(vector.hash);
      expect(JSON.parse(JSON.stringify(profile)), vector.name).toEqual(vector.finalProfile);
      expect(JSON.parse(JSON.stringify(cast)), vector.name).toEqual(vector.finalCast);
      expect(terminal(cast)).toBe(true);
      if (cast.reward) {
        const again = resolveTreasure(profile, cast, () => {
          throw Error('reward drew twice');
        });
        expect(again.profile).toEqual(profile);
        expect(again.cast).toEqual(cast);
      }
    }
  });
  it('matches original shop, skill, basket and contract actions', () => {
    expect(actions.actions).toHaveLength(89);
    for (const vector of actions.actions) {
      const r = applyAction(vector.profile as Profile, vector.action as Action);
      expect(JSON.parse(JSON.stringify(r.profile)), vector.name).toEqual(vector.finalProfile);
      expect(r.coin_delta).toBe(vector.coinDelta);
    }
  });
  it('rejects nonfinite restoration, bad random and unsafe integer assets', () => {
    const profile = initialProfile();
    expect(Object.keys(catalog.GEAR)).toHaveLength(14);
    expect(catalog.FISH_TYPES).toHaveLength(54);
    const { cast } = start(profile, () => 0, 1);
    expect(() => validateCast({ ...cast, barVelocity: Infinity })).toThrow();
    expect(() => start(profile, () => 1, 1)).toThrow();
    expect(() => advance(profile, { ...cast, paused: true }, [false])).toThrow();
    expect(() => advance(profile, cast, Array<boolean>(121).fill(false))).toThrow();
    for (const amount of ['00', '-1', '1.5', '340282366920938463463374607431768211456'])
      expect(() => parseAmount(amount)).toThrow();
    expect(parseAmount('340282366920938463463374607431768211455')).toBe((1n << 128n) - 1n);
    const terminalCast = structuredClone(games.vectors[0].finalCast) as Cast;
    expect(() => validateCast(terminalCast)).not.toThrow();
  });
  it('claims earned current-day and cross-day quests exactly once without generating a board', () => {
    for (const slot of ['cleanup', 'delivery1']) {
      for (const nextDay of [false, true]) {
        for (const currentBoard of [false, true]) {
          let profile = initialProfile();
          ensureContractBoard(profile);
          const id = `1-${slot}`;
          profile = applyAction(profile, { action: 'accept_contract', id }).profile;
          if (nextDay) advanceClock(profile, 1440);
          if (currentBoard) ensureContractBoard(profile);
          if (slot === 'cleanup') {
            for (let i = 0; i < 4; i++) advanceContracts(profile, 'cleanup');
          } else {
            for (let i = 0; i < 3; i++) storeCatch(profile, fish('gold')!, 20, 0, false);
          }
          const before = structuredClone(profile);
          const result = applyAction(profile, { action: 'claim_contract', id });
          expect(profile).toEqual(before);
          expect(result.profile.coins).toBe(slot === 'cleanup' ? '140' : '119');
          expect(result.coin_delta).toBe(result.profile.coins);
          expect(result.profile.baitStock.basic).toBe(slot === 'cleanup' ? 5 : 3);
          expect(result.profile.completedContracts).toBe('1');
          expect(result.profile.basket).toHaveLength(0);
          expect(result.profile.contractsDay).toBe(before.contractsDay);
          expect(result.profile.contracts).toHaveLength(before.contracts.length - Number(nextDay));
          expect(result.profile.contracts.find((quest) => quest.id === id)).toEqual(
            nextDay
              ? undefined
              : { ...before.contracts.find((quest) => quest.id === id), status: 'completed' },
          );
          expect(() => validateProfile(result.profile)).not.toThrow();
          const claimed = structuredClone(result.profile);
          expect(() => applyAction(result.profile, { action: 'claim_contract', id })).toThrow();
          expect(result.profile).toEqual(claimed);
          profile.coins = '340282366920938463463374607431768211455';
          const overflow = structuredClone(profile);
          expect(() => applyAction(profile, { action: 'claim_contract', id })).toThrow();
          expect(profile).toEqual(overflow);
        }
      }
    }
  });
  it('validates incoming segments before prediction and preserves rejected inputs', () => {
    const profile = initialProfile();
    const { cast } = start(profile, () => 0, 1);
    const malformed = { ...cast, barVelocity: Infinity };
    const before = structuredClone(malformed);
    expect(() => advance(profile, malformed, [false])).toThrow();
    expect(malformed).toEqual(before);
    expect(profile).toEqual(initialProfile());
  });
});
