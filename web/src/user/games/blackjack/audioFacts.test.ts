import { describe, expect, it } from 'vitest';
import { blackjackState } from '@shared/games/blackjack';
import { blackjackAudioFacts } from './audioFacts';
import { blackjackWire, tableID } from './testFixtures';

function copy<T>(value: T): T {
  return structuredClone(value);
}

function state(phase: 'seating' | 'decision' | 'result' = 'decision') {
  return blackjackState(blackjackWire(phase));
}

type BlackjackWire = ReturnType<typeof blackjackWire>;
function tableCards(wire: BlackjackWire) {
  if (!wire.table.fact.cards) throw new Error('fixture table has no cards');
  return wire.table.fact.cards;
}

describe('blackjack authoritative audio facts', () => {
  it('uses actual hand and dealer diffs for deal, flip, double, split, natural and bust', () => {
    const before = state();
    const hitWire = copy(blackjackWire());
    hitWire.server_now++;
    hitWire.table.revision = '2';
    tableCards(hitWire).seats[0].hands[0].cards.push({ rank: 2, suit: 1 });
    tableCards(hitWire).seats[0].hands[0].total.value = 18;
    tableCards(hitWire).seats[0].hands[0].revision = '2';
    const hit = blackjackState(hitWire);
    const hitFacts = blackjackAudioFacts(hit, before);
    expect(hitFacts.some((fact) => fact.cue === 'blackjack_deal')).toBe(true);
    expect(hitFacts.some((fact) => fact.cue === 'blackjack_double')).toBe(false);

    const doubleWire = copy(blackjackWire());
    doubleWire.server_now++;
    doubleWire.table.revision = '2';
    tableCards(doubleWire).seats[0].hands[0].units = 2;
    tableCards(doubleWire).seats[0].hands[0].revision = '2';
    const doubled = blackjackState(doubleWire);
    expect(
      blackjackAudioFacts(doubled, before).some((fact) => fact.cue === 'blackjack_double'),
    ).toBe(true);

    const splitWire = copy(blackjackWire());
    splitWire.server_now++;
    splitWire.table.revision = '2';
    tableCards(splitWire).seats[0].hands[0].split = true;
    tableCards(splitWire).seats[0].hands.push({
      ...tableCards(splitWire).seats[0].hands[0],
      revision: '2',
      split: true,
    });
    const split = blackjackState(splitWire);
    expect(blackjackAudioFacts(split, before).some((fact) => fact.cue === 'blackjack_split')).toBe(
      true,
    );

    const naturalWire = copy(blackjackWire());
    naturalWire.server_now++;
    naturalWire.table.revision = '2';
    tableCards(naturalWire).seats[0].hands[0].natural = true;
    tableCards(naturalWire).seats[0].hands[0].total.value = 21;
    tableCards(naturalWire).seats[0].hands[0].revision = '2';
    const natural = blackjackState(naturalWire);
    expect(
      blackjackAudioFacts(natural, before).some((fact) => fact.cue === 'blackjack_natural'),
    ).toBe(true);

    const bustWire = copy(blackjackWire());
    bustWire.server_now++;
    bustWire.table.revision = '2';
    tableCards(bustWire).seats[0].hands[0].cards.push({ rank: 6, suit: 1 });
    tableCards(bustWire).seats[0].hands[0].total.value = 22;
    tableCards(bustWire).seats[0].hands[0].revision = '2';
    const bust = blackjackState(bustWire);
    expect(blackjackAudioFacts(bust, before).some((fact) => fact.cue === 'blackjack_bust')).toBe(
      true,
    );

    const flipWire = copy(blackjackWire());
    flipWire.server_now++;
    flipWire.table.revision = '2';
    tableCards(flipWire).hole_hidden = false;
    tableCards(flipWire).dealer.push({ rank: 6, suit: 1 });
    const flip = blackjackState(flipWire);
    const flipFacts = blackjackAudioFacts(flip, before);
    expect(flipFacts.some((fact) => fact.cue === 'blackjack_flip')).toBe(true);
    expect(flipFacts.filter((fact) => fact.cue === 'blackjack_deal')).toHaveLength(0);

    const standWire = copy(blackjackWire());
    standWire.server_now++;
    standWire.table.revision = '2';
    tableCards(standWire).seats[0].hands[0].stood = true;
    tableCards(standWire).seats[0].hands[0].revision = '2';
    const stood = blackjackState(standWire);
    expect(blackjackAudioFacts(stood, before).some((fact) => fact.cue === 'common_confirm')).toBe(
      true,
    );

    const autoStandDoubleWire = copy(blackjackWire());
    autoStandDoubleWire.server_now++;
    autoStandDoubleWire.table.revision = '2';
    tableCards(autoStandDoubleWire).seats[0].hands[0].units = 2;
    tableCards(autoStandDoubleWire).seats[0].hands[0].stood = true;
    tableCards(autoStandDoubleWire).seats[0].hands[0].revision = '2';
    const autoStandDouble = blackjackState(autoStandDoubleWire);
    const autoStandDoubleFacts = blackjackAudioFacts(autoStandDouble, before);
    expect(autoStandDoubleFacts.some((fact) => fact.cue === 'blackjack_double')).toBe(true);
    expect(autoStandDoubleFacts.some((fact) => fact.cue === 'common_confirm')).toBe(false);

    const autoStandBustWire = copy(blackjackWire());
    autoStandBustWire.server_now++;
    autoStandBustWire.table.revision = '2';
    tableCards(autoStandBustWire).seats[0].hands[0].cards.push({ rank: 6, suit: 1 });
    tableCards(autoStandBustWire).seats[0].hands[0].total.value = 22;
    tableCards(autoStandBustWire).seats[0].hands[0].stood = true;
    tableCards(autoStandBustWire).seats[0].hands[0].revision = '2';
    const autoStandBust = blackjackState(autoStandBustWire);
    const autoStandBustFacts = blackjackAudioFacts(autoStandBust, before);
    expect(autoStandBustFacts.some((fact) => fact.cue === 'blackjack_bust')).toBe(true);
    expect(autoStandBustFacts.some((fact) => fact.cue === 'common_confirm')).toBe(false);
  });

  it('emits own result once and keeps natural separate from ordinary win', () => {
    const before = state();
    const result = state('result');
    const facts = blackjackAudioFacts(result, before);
    expect(facts.some((fact) => fact.cue === 'common_win')).toBe(true);
    expect(
      facts.every(
        (fact) =>
          fact.at === result.table!.terminal_at! * 1000 ||
          fact.cue === 'blackjack_flip' ||
          fact.cue === 'blackjack_deal',
      ),
    ).toBe(true);
    expect(blackjackAudioFacts(result, result)).toEqual([]);

    const naturalWire = copy(blackjackWire('result'));
    tableCards(naturalWire).seats[0].hands[0].natural = true;
    tableCards(naturalWire).seats[0].hands[0].total.value = 21;
    tableCards(naturalWire).seats[0].hands[0].cards = [
      { rank: 1, suit: 0 },
      { rank: 13, suit: 1 },
    ];
    naturalWire.table.fact.settlements[0].hands[0].outcome = 'natural';
    const natural = blackjackState(naturalWire);
    const naturalFacts = blackjackAudioFacts(natural, before);
    expect(naturalFacts.some((fact) => fact.cue === 'blackjack_natural')).toBe(true);
    expect(naturalFacts.some((fact) => fact.cue === 'common_win')).toBe(false);
  });

  it('recognizes a natural deal after seating and aggregates split settlement net deltas', () => {
    const seating = state('seating');
    const before = state();
    const naturalWire = copy(blackjackWire());
    naturalWire.server_now++;
    tableCards(naturalWire).seats[0].hands[0].natural = true;
    tableCards(naturalWire).seats[0].hands[0].total.value = 21;
    const natural = blackjackState(naturalWire);
    const naturalFacts = blackjackAudioFacts(natural, seating);
    expect(naturalFacts.filter((fact) => fact.cue === 'blackjack_natural')).toHaveLength(1);

    const mixedWire = copy(blackjackWire('result'));
    tableCards(mixedWire).seats[0].hands.push({
      ...tableCards(mixedWire).seats[0].hands[0],
      split: true,
      revision: '4',
    });
    mixedWire.table.fact.settlements[0].hands = [
      mixedWire.table.fact.settlements[0].hands[0],
      {
        ...mixedWire.table.fact.settlements[0].hands[0],
        stake_milli: '10000000',
        net_milli: '0',
        outcome: 'loss',
      },
    ];
    const mixed = blackjackState(mixedWire);
    const mixedFacts = blackjackAudioFacts(mixed, before);
    expect(mixedFacts.some((fact) => fact.cue === 'common_loss')).toBe(true);
    expect(mixedFacts.some((fact) => fact.cue === 'common_win')).toBe(false);

    for (const hand of mixedWire.table.fact.settlements[0].hands) {
      hand.stake_milli = '5000000';
      hand.gross_milli = hand.outcome === 'loss' ? '0' : '10000000';
      hand.net_milli = hand.gross_milli;
      hand.platform_milli = hand.welfare_milli = hand.thursday_milli = '0';
    }
    const evenFacts = blackjackAudioFacts(blackjackState(mixedWire), before);
    expect(evenFacts.some((fact) => fact.cue === 'common_draw')).toBe(true);
    expect(evenFacts.some((fact) => fact.cue === 'common_loss')).toBe(false);
  });

  it('matches a new table when seating rolls into its first dealt snapshot', () => {
    const seating = state('seating');
    const dealtWire = copy(blackjackWire());
    dealtWire.table.id = `${tableID.slice(0, -1)}Q`;
    const dealt = blackjackState(dealtWire);
    const facts = blackjackAudioFacts(dealt, seating);
    expect(facts.some((fact) => fact.cue === 'common_match')).toBe(true);
    expect(facts.some((fact) => fact.cue === 'blackjack_deal')).toBe(true);
  });

  it('does not invent facts from a first snapshot', () => {
    expect(blackjackAudioFacts(state())).toEqual([]);
    expect(blackjackAudioFacts(state('result'))).toEqual([]);
  });
});
