import { describe, expect, it } from 'vitest';
import { normalizeRPSPending, normalizeRPSState } from './normalize';
import { rpsStateWire, rpsTestSessionID } from './testFixtures';

function pending() {
  return {
    session_id: rpsTestSessionID,
    mode: 'quick',
    terminal_reason: 'quick_resolved',
    own_seat_no: 0,
    own_input: '42',
    own_returned: '44.003',
    own_wallet_net: '2.003',
    own_buy_in: '18.002' as string | null,
    own_cash_out: '20.005' as string | null,
    seats: [
      { seat_no: 0, result: 'win', gesture: 'rock' as string | null },
      { seat_no: 1, result: 'loss', gesture: 'scissors' as string | null },
      { seat_no: 2, result: 'loss', gesture: 'scissors' as string | null },
    ],
    created_at: 1_800_000_000,
  };
}

describe('authoritative RPS presentation', () => {
  it.each([null, '0', '7', ((1n << 128n) - 1n).toString()])(
    'preserves the exact nullable pool counter %s',
    (count) => {
      const state = rpsStateWire();
      Object.assign(state.round_summary, { pool_tie_count: count });
      expect(normalizeRPSState(state).roundSummary.poolTieCount).toBe(count);
    },
  );

  it.each([undefined, 0, '-1', '01', (1n << 128n).toString()])(
    'rejects an invalid pool counter %s',
    (count) => {
      const state = rpsStateWire();
      Object.assign(state.round_summary, { pool_tie_count: count });
      expect(() => normalizeRPSState(state)).toThrow();
    },
  );

  it('keeps anonymous revealed gestures and only the viewer wallet transfers', () => {
    const value = pending();
    value.seats[1].result = 'deidentified';
    expect(normalizeRPSPending(value)).toMatchObject({
      ownBuyIn: '18.002',
      ownCashOut: '20.005',
      seats: [
        { seatNo: 0, gesture: 'rock' },
        { seatNo: 1, gesture: 'scissors', result: 'deidentified' },
        { seatNo: 2, gesture: 'scissors' },
      ],
    });
    Object.assign(value.seats[1], { own_buy_in: '1' });
    expect(() => normalizeRPSPending(value)).toThrow();
  });

  it('preserves unknown historical fields without inventing zero or gestures', () => {
    const value = pending();
    value.own_buy_in = value.own_cash_out = null;
    for (const seat of value.seats) seat.gesture = null;
    expect(normalizeRPSPending(value)).toMatchObject({
      ownBuyIn: null,
      ownCashOut: null,
      seats: [{ gesture: null }, { gesture: null }, { gesture: null }],
    });
    value.mode = 'standard';
    value.terminal_reason = 'standard_round_limit';
    expect(normalizeRPSPending(value).seats.every((seat) => seat.gesture === null)).toBe(true);
  });

  it('retains wide cumulative amounts alongside bounded wallet transfers', () => {
    const value = pending();
    const recycled = 1n << 128n;
    value.own_input = recycled.toString();
    value.own_returned = (recycled + 1n).toString();
    value.own_wallet_net = '1';
    value.own_buy_in = '5';
    value.own_cash_out = '6';
    expect(normalizeRPSPending(value)).toMatchObject({
      ownInput: recycled.toString(),
      ownReturned: (recycled + 1n).toString(),
      ownBuyIn: '5',
      ownCashOut: '6',
    });
  });

  it.each([
    (value: ReturnType<typeof pending>) => {
      value.own_buy_in = null;
    },
    (value: ReturnType<typeof pending>) => {
      value.own_cash_out = '20';
    },
    (value: ReturnType<typeof pending>) => {
      value.own_cash_out = (1n << 128n).toString();
    },
    (value: ReturnType<typeof pending>) => {
      value.seats[1].gesture = null;
    },
    (value: ReturnType<typeof pending>) => {
      value.seats[1].gesture = 'unknown';
    },
    (value: ReturnType<typeof pending>) => {
      value.mode = 'standard';
      value.terminal_reason = 'standard_round_limit';
    },
  ])('rejects incomplete, inconsistent or unrevealed presentation', (change) => {
    const value = pending();
    change(value);
    expect(() => normalizeRPSPending(value)).toThrow();
  });
});
