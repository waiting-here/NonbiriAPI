import { describe, expect, it } from 'vitest';
import { normalizeGamesSnapshot } from './snapshot';
import { gamesSnapshotWire } from './testFixtures';

describe('games snapshot normalizer', () => {
  it('keeps closed sub-capabilities and large exact balances distinct from empty data', () => {
    const value = normalizeGamesSnapshot(gamesSnapshotWire());
    expect(value.balance).toBe('12345678901234567890.125');
    expect(value.linklink.specs['8x8'].enabled).toBe(false);
    expect(value.rps.modes.deathmatch.base).toBe('3');
  });

  it('accepts canonical zero prices only for disabled LinkLink and RPS capabilities', () => {
    const disabled = gamesSnapshotWire();
    disabled.linklink.enabled = false;
    disabled.rps.enabled = false;
    for (const spec of Object.values(disabled.linklink.specs)) {
      spec.enabled = false;
      spec.price = '0';
    }
    for (const mode of Object.values(disabled.rps.modes)) {
      mode.enabled = false;
      mode.base = '0';
    }
    expect(normalizeGamesSnapshot(disabled).rps.modes.quick.base).toBe('0');

    disabled.linklink.specs['6x8'].enabled = true;
    expect(() => normalizeGamesSnapshot(disabled)).toThrow(/enabled price|capability matrix/i);
    disabled.linklink.specs['6x8'].enabled = false;
    disabled.rps.modes.quick.enabled = true;
    expect(() => normalizeGamesSnapshot(disabled)).toThrow(/enabled base|capability matrix/i);
  });

  it('preserves independently configured child gates while a parent or master gate is closed', () => {
    const layered = gamesSnapshotWire();
    layered.games_enabled = false;
    layered.linklink.enabled = false;
    layered.rps.enabled = false;
    const parsed = normalizeGamesSnapshot(layered);
    expect(parsed.gamesEnabled).toBe(false);
    expect(parsed.fishing.enabled).toBe(true);
    expect(parsed.linklink.specs['6x8'].enabled).toBe(true);
    expect(parsed.rps.modes.quick.enabled).toBe(true);
  });

  it('accepts the signed SM128 account balance range and rejects overflow', () => {
    const boundary = gamesSnapshotWire();
    boundary.balance = '-170141183460469231731687303715884105.727';
    expect(normalizeGamesSnapshot(boundary).balance).toBe(boundary.balance);
    boundary.balance = '170141183460469231731687303715884105.728';
    expect(() => normalizeGamesSnapshot(boundary)).toThrow(/snapshot balance/i);
  });

  it('requires all nine ordered reward facts and a consistent completion summary', () => {
    const wire = gamesSnapshotWire();
    expect(normalizeGamesSnapshot(wire).onboarding.linklink.items.map((item) => item.reward)).toEqual(['1000', '2000', '3000']);
    wire.onboarding.rps.items[0].completed = true;
    expect(normalizeGamesSnapshot(wire).onboarding.rps.items[0].completed).toBe(true);
    const wrongReward = structuredClone(wire);
    wrongReward.onboarding.rps.items[0].reward = '2000';
    const duplicate = structuredClone(wire);
    duplicate.onboarding.rps.items[1].key = 'quick';
    const incomplete = structuredClone(wire);
    incomplete.onboarding.fishing.items.pop();
    const wrongAsset = structuredClone(wire);
    wrongAsset.onboarding.fishing.items[0].asset_type = 'game';
    const inconsistent = structuredClone(wire);
    inconsistent.onboarding.rps.all_completed = true;
    for (const invalid of [wrongReward, duplicate, incomplete, wrongAsset, inconsistent])
      expect(() => normalizeGamesSnapshot(invalid)).toThrow(/onboarding/i);
  });

  it('fails closed on unknown keys, noncanonical money, and invalid pump totals', () => {
    const extra = { ...gamesSnapshotWire(), extra: true };
    expect(() => normalizeGamesSnapshot(extra)).toThrow(/invalid game data/i);
    const money = gamesSnapshotWire();
    money.fishing.bait_prices.worm = '1.0';
    expect(() => normalizeGamesSnapshot(money)).toThrow(/invalid game data/i);
    const pumps = gamesSnapshotWire();
    pumps.rps.modes.quick.pumps_bp = { platform: 9_999, welfare: 1, thursday: 0 };
    expect(() => normalizeGamesSnapshot(pumps)).toThrow(/pump total/i);
    const capacity = gamesSnapshotWire();
    capacity.rps.modes.quick.queue_capacity = 4_095;
    expect(() => normalizeGamesSnapshot(capacity)).toThrow(/capacity/i);
  });
});
