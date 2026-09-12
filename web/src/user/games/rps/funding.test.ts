import { describe, expect, it } from 'vitest';
import { normalizeRPSPending, normalizeRPSQueue, normalizeRPSState } from './normalize';
import { rpsStateWire, rpsTestSessionID } from './testFixtures';

function mixedState() {
  const state = rpsStateWire();
  state.rule_snapshot.rules_version = 2;
  return {
    ...state,
    seats: [{
      ...state.seats[0],
      funding: { buy_in_general: '8', buy_in_game: '2', current_general: '8', game_remaining: '1' },
    }, state.seats[1], state.seats[2]] as const,
  };
}

function pending() {
  return {
    rules_version: 2,
    session_id: rpsTestSessionID, mode: 'standard', terminal_reason: 'standard_round_limit',
    own_seat_no: 0, own_input: '7', own_returned: '8', own_wallet_net: '1',
    own_buy_in: '5', own_cash_out: '6',
    own_buy_in_general: '2', own_buy_in_game: '3', own_returned_general: '6',
    seats: [
      { seat_no: 0, result: 'win', gesture: null },
      { seat_no: 1, result: 'loss', gesture: null },
      { seat_no: 2, result: 'deidentified', gesture: null },
    ],
    created_at: 1_800_000_000,
  };
}

describe('RPS payment sources', () => {
  it('preserves the original queue payment and only the viewing seat funding', () => {
    expect(normalizeRPSQueue({
      rules_version: 2, payment: { general: '2', game: '3' },
      id: 'rpsq_AAAAAAAAAAAAAAAAAAAAAA', mode: 'standard', state: 'waiting',
      revision: '1', deadline: 1_800_000_010, server_now: 1_800_000_000,
    }).payment).toEqual({ general: '2', game: '3' });
    const state = normalizeRPSState(mixedState());
    expect(state.seats[0]).toMatchObject({
      funding: { buyInGeneral: '8', buyInGame: '2', currentGeneral: '8', gameRemaining: '1' },
    });
    expect(state.seats[1]).not.toHaveProperty('funding');
    const leaked = mixedState();
    Object.assign(leaked.seats[1], { funding: leaked.seats[0].funding });
    expect(() => normalizeRPSState(leaked)).toThrow();
  });

  it('requires complete current sources and rejects invented game principal', () => {
    const missing = rpsStateWire();
    missing.rule_snapshot.rules_version = 2;
    expect(() => normalizeRPSState(missing)).toThrow();
    const invalid = mixedState();
    Object.assign(invalid.seats[0].funding, { game_remaining: '2' });
    expect(() => normalizeRPSState(invalid)).toThrow();
  });

  it('keeps terminal cash-out separate from cumulative round returns', () => {
    expect(normalizeRPSPending(pending())).toMatchObject({
      ownBuyInGeneral: '2', ownBuyInGame: '3', ownReturnedGeneral: '6', ownReturned: '8',
    });
    expect(() => normalizeRPSPending({ ...pending(), own_returned_general: '8' })).toThrow();
    expect(() => normalizeRPSPending({ ...pending(), own_buy_in_game: null })).toThrow();
    expect(normalizeRPSPending({
      ...pending(), rules_version: 1, own_buy_in_general: null, own_buy_in_game: null, own_returned_general: null,
    }).ownBuyInGeneral).toBeNull();
  });
});
