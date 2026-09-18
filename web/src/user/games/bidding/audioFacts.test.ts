import { describe, expect, it } from 'vitest';
import { homeValue } from '../common/duel/normalize';
import { biddingCodec } from './normalize';
import { biddingAudioFacts } from './audioFacts';
import { biddingHomeWire } from './testFixtures';

type BiddingWire = ReturnType<typeof biddingHomeWire>;
type MutableBiddingWire = Omit<BiddingWire, 'current' | 'latest_result'> & {
  current: BiddingWire['current'] | null;
  latest_result: unknown;
};

function mutableWire(): MutableBiddingWire {
  return biddingHomeWire() as MutableBiddingWire;
}

function normalized(wire: unknown) {
  return homeValue(wire, biddingCodec);
}

function progressedWire() {
  const wire = biddingHomeWire();
  wire.server_now += 1;
  wire.current.server_now += 1;
  wire.current.revision = '2';
  wire.current.phase_seq = '2';
  wire.current.locked = [true, false];
  wire.current.view.joker_available = [false, true];
  wire.current.view.played = [[13], [13]];
  wire.current.view.hand_remaining = [
    Array.from({ length: 12 }, (_, index) => index + 1),
    Array.from({ length: 12 }, (_, index) => index + 1),
  ];
  wire.current.view.rewards = [
    ...wire.current.view.rewards,
    { round: 2, side: 0, rank: 13, multiplier: 2, status: 'pool', owner: null },
    { round: 2, side: 1, rank: 4, multiplier: 1, status: 'pool', owner: null },
  ];
  return wire;
}

describe('bidding authoritative audio facts', () => {
  it('emits reveal, king, pot add and lock only for a real current transition', () => {
    const before = normalized(biddingHomeWire());
    const after = normalized(progressedWire());
    const facts = biddingAudioFacts(after, before);
    expect(new Set(facts.map((fact) => fact.cue))).toEqual(
      new Set(['common_lock', 'bidding_reveal', 'bidding_king', 'bidding_pot_add']),
    );
    expect(facts.filter((fact) => fact.cue === 'bidding_pot_add')).toHaveLength(2);
    expect(facts.find((fact) => fact.cue === 'common_lock')?.key).toContain(
      `:${after.current!.round}:${after.current!.phaseSeq}:lock:0`,
    );
    expect(facts.every((fact) => fact.at === after.current!.serverNow * 1000)).toBe(true);
    expect(biddingAudioFacts(after, before)).toEqual(facts);
  });

  it('collects awarded pots and never turns a discarded end reward into a collect', () => {
    const before = normalized(progressedWire());
    const awardedWire = progressedWire();
    awardedWire.server_now += 1;
    awardedWire.current.server_now += 1;
    awardedWire.current.revision = '3';
    awardedWire.current.view.rewards = awardedWire.current.view.rewards.map((reward) =>
      reward.round === 2 && reward.side === 0
        ? { ...reward, status: 'awarded' as const, owner: 0 as const }
        : reward.round === 2 && reward.side === 1
          ? { ...reward, status: 'discarded' as const, owner: null }
          : reward,
    );
    const awarded = normalized(awardedWire);
    const facts = biddingAudioFacts(awarded, before);
    expect(facts.filter((fact) => fact.cue === 'bidding_pot_collect')).toHaveLength(1);
    expect(
      facts.some((fact) => fact.cue === 'bidding_pot_collect' && fact.key.endsWith(':1')),
    ).toBe(false);
  });

  it('recognizes a newly matched session and terminal outcomes, while cancelling silently', () => {
    const initial = normalized(biddingHomeWire());
    const matchedRaw = biddingHomeWire();
    const matched = normalized(matchedRaw);
    const noCurrentRaw = mutableWire();
    noCurrentRaw.current = null;
    noCurrentRaw.latest_result = {
      id: 'bid_AAAAAAAAAAAAAAAAAAAAAA',
      game: 'bidding',
      mode: 'tier1',
      terminal_at: noCurrentRaw.server_now + 1,
      outcome: 'win',
      reason: 'rounds',
      scores: [10, 9],
      own_payment: { general: '0', game: '0' },
      own_refund: { general: '0', game: '0' },
      prize_general: '1',
      rake: { platform: '0', welfare: '0', thursday: '0' },
      you: 0,
      profiles: [{ kind: 'anonymous' }, { kind: 'anonymous' }],
      view: null,
      resolution: null,
    };
    const terminal = normalized(noCurrentRaw);
    expect(biddingAudioFacts(matched, { ...initial, current: null })).toEqual([
      {
        key: `bidding:${matched.current!.id}:match`,
        cue: 'common_match',
        at: matched.current!.serverNow * 1000,
      },
    ]);
    expect(biddingAudioFacts(terminal, initial)).toEqual([
      {
        key: `bidding:${terminal.latestResult!.id}:result:terminal`,
        cue: 'common_win',
        at: terminal.latestResult!.terminalAt * 1000,
      },
    ]);
    const cancelledRaw = {
      ...noCurrentRaw,
      latest_result: {
        ...(noCurrentRaw.latest_result as Record<string, unknown>),
        outcome: 'system_cancelled',
      },
    };
    expect(biddingAudioFacts(normalized(cancelledRaw), initial)).toEqual([]);
  });

  it('does not diff a terminal view from a different session', () => {
    const previous = normalized(biddingHomeWire());
    const staleRaw = mutableWire();
    const staleView = progressedWire().current.view;
    staleRaw.current = null;
    staleRaw.latest_result = {
      id: 'bid_AQEBAQEBAQEBAQEBAQEBAQ',
      game: 'bidding',
      mode: 'tier1',
      terminal_at: staleRaw.server_now + 1,
      outcome: 'win',
      reason: 'rounds',
      scores: [10, 9],
      own_payment: { general: '0', game: '0' },
      own_refund: { general: '0', game: '0' },
      prize_general: '1',
      rake: { platform: '0', welfare: '0', thursday: '0' },
      you: 0,
      profiles: [{ kind: 'anonymous' }, { kind: 'anonymous' }],
      view: staleView,
      resolution: null,
    };
    const stale = normalized(staleRaw);
    expect(biddingAudioFacts(stale, previous)).toEqual([
      {
        key: `bidding:${stale.latestResult!.id}:result:terminal`,
        cue: 'common_win',
        at: stale.latestResult!.terminalAt * 1000,
      },
    ]);
  });
});
