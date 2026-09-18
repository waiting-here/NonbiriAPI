import { describe, expect, it } from 'vitest';
import { configValue, homeValue } from '../common/duel/normalize';
import { biddingCodec, biddingView } from './normalize';
import { biddingHomeWire, biddingViewWire } from './testFixtures';

describe('bidding public boundary', () => {
  it('accepts the eight-field projection and keeps both hands until revelation', () => {
    const wire = biddingHomeWire();
    wire.current.locked[0] = true;
    wire.current.view.own_selected_card = 13;
    const result = homeValue(wire, biddingCodec);
    expect(result.current?.view.hands[0]).toHaveLength(13);
    expect(result.current?.view.selected).toBe(13);
    expect(result.current?.profiles[1].displayName).toBe('Card player');
  });
  it('rejects private decks, duplicate cards, extra selected seats and unpaired rewards', () => {
    const base = biddingViewWire();
    for (const invalid of [
      { ...base, decks: [[1], [2]] },
      { ...base, opponent_selected_card: 13 },
      { ...base, hand_remaining: [Array(13).fill(1), base.hand_remaining[1]] },
      { ...base, rewards: base.rewards.slice(0, 1) },
    ])
      expect(() => biddingView(invalid)).toThrow();
  });
  it('rejects invalid revisions, null deadlines and hidden identity fields', () => {
    const base = biddingHomeWire();
    for (const change of [
      { revision: 1 },
      { phase_seq: '0' },
      { deadline: null },
      { profiles: [{ kind: 'anonymous', user_id: '1' }, { kind: 'anonymous' }] },
    ])
      expect(() =>
        homeValue({ ...base, current: { ...base.current, ...change } }, biddingCodec),
      ).toThrow();
  });
  it('rejects contradictory slots and non-bidding phases', () => {
    const base = biddingHomeWire();
    expect(() => homeValue({ ...base, queue: {} }, biddingCodec)).toThrow();
    expect(() =>
      homeValue({ ...base, current: { ...base.current, phase: 'settlement' } }, biddingCodec),
    ).toThrow();
  });
  it('requires complete admission terms and rejects unaffordable precision truncation', () => {
    const mode = {
      enabled: true,
      available: true,
      ticket: '9000000000000',
      rake_bp: { platform: 1, welfare: 2, thursday: 3 },
      terms_hash: 'a'.repeat(64),
      content_hash: 'b'.repeat(64),
    };
    const config = {
      enabled: true,
      available: true,
      modes: { tier1: mode, tier2: mode, tier3: mode },
      queue_seconds: 120,
      queue_capacity: 4096,
      joker_seconds: 10,
      bid_seconds: 20,
    };
    expect(configValue(config, 'bidding', biddingCodec.modes).modes.tier1.ticket).toBe(
      '9000000000000',
    );
    expect(() =>
      configValue(
        { ...config, modes: { ...config.modes, tier1: { ...mode, ticket: '9000000000000.001' } } },
        'bidding',
        biddingCodec.modes,
      ),
    ).toThrow();
    expect(() =>
      configValue({ ...config, queue_seconds: 121 }, 'bidding', biddingCodec.modes),
    ).toThrow();
  });
});
