import { describe, expect, it } from 'vitest';
import {
  normalizeRPSHome,
  normalizeRPSLeaderboard,
  normalizeRPSState,
  shouldApplyRPSReplacement,
} from './normalize';
import type { RPSPhase } from './types';
import {
  rpsSeatWire as seat,
  rpsStateWire as stateWire,
  rpsTestSessionID as sessionID,
} from './testFixtures';

const phases: readonly RPSPhase[] = [
  'gesture',
  'dealer_raise',
  'followers',
  'paid_pool_gesture',
  'free_pool_gesture',
  'ultimate_gesture',
  'terminal_processing',
];
function pendingHome() {
  return normalizeRPSHome({
    kind: 'pending_result',
    result: {
      session_id: sessionID,
      mode: 'standard',
      terminal_reason: 'standard_round_limit',
      own_seat_no: 0,
      own_input: '5',
      own_returned: '6',
      own_wallet_net: '1',
      seats: [
        { seat_no: 0, result: 'win' },
        { seat_no: 1, result: 'loss' },
        { seat_no: 2, result: 'tie' },
      ],
      created_at: 1_800_000_000,
    },
  });
}

describe('RPS strict viewer projection', () => {
  it('accepts the canonical rpsq queue identity and rejects lookalike prefixes', () => {
    const queue = {
      kind: 'queue',
      queue: {
        id: 'rpsq_AAAAAAAAAAAAAAAAAAAAAA',
        mode: 'quick',
        state: 'waiting',
        revision: '1',
        deadline: 1_800_000_010,
        server_now: 1_800_000_000,
      },
    };
    expect(normalizeRPSHome(queue)).toMatchObject({
      kind: 'queue',
      queue: { id: 'rpsq_AAAAAAAAAAAAAAAAAAAAAA' },
    });
    const current = normalizeRPSHome(queue);
    const mode = {
      enabled: true,
      base: '1',
      pumps_bp: { platform: 100, welfare: 50, thursday: 0 },
      queue_seconds: 60,
      gesture_seconds: 10,
      dealer_seconds: 8,
      follower_seconds: 8,
      queue_capacity: 4096,
    };
    expect(
      shouldApplyRPSReplacement(
        current,
        normalizeRPSHome({
          kind: 'idle',
          tutorial_seen: true,
          modes: { quick: mode, standard: mode, deathmatch: mode },
        }),
      ),
    ).toBe(true);
    queue.queue.id = 'rpq_AAAAAAAAAAAAAAAAAAAAAA';
    expect(() => normalizeRPSHome(queue)).toThrow(/queue id/i);
  });

  it.each(phases)('accepts the exact %s phase matrix and one legal actor option', (phase) => {
    const state = normalizeRPSState(stateWire(phase));
    expect(state.phase).toBe(phase);
    expect(state.currentActorOptions).toHaveLength(phase === 'terminal_processing' ? 0 : 1);
  });

  it('accepts no-raise followers and signed settlement event deltas', () => {
    const followers = stateWire('followers');
    followers.economy.dealer_raise = null;
    expect(normalizeRPSState(followers).economy.dealerRaise).toBeNull();

    const settled = stateWire();
    Object.assign(settled, {
      recent_events: [
        {
          seq: '1',
          identity_epoch: '1',
          kind: 'settlement',
          phase_seq: '1',
          safe_payload: {
            seat_deltas: [
              { seat_no: 0, delta: '-1' },
              { seat_no: 1, delta: '0.5' },
              { seat_no: 2, delta: '0.5' },
            ],
            player_pool: '0',
            cuts: { platform: '0', welfare: '0', thursday: '0' },
          },
        },
      ],
    });
    settled.first_available_seq = '1';
    expect(normalizeRPSState(settled).recentEvents[0].kind).toBe('settlement');
  });

  it('accepts a size-truncated projection whose recent-event list was fully removed', () => {
    const truncated = stateWire();
    truncated.events_truncated = true;
    truncated.first_available_seq = '7';
    expect(normalizeRPSState(truncated)).toMatchObject({
      recentEvents: [],
      eventsTruncated: true,
      firstAvailableSeq: '7',
    });

    truncated.first_available_seq = '0';
    expect(() => normalizeRPSState(truncated)).toThrow(/empty event sequence/i);
  });

  it('rejects hidden gesture fields and phase-incompatible action options', () => {
    const hidden = stateWire();
    Object.assign(hidden.seats[1], { current_gesture: 'rock' });
    expect(() => normalizeRPSState(hidden)).toThrow(/seat 1/i);
    const wrong = stateWire('dealer_raise');
    wrong.current_actor_options = ['gesture'];
    expect(() => normalizeRPSState(wrong)).toThrow(/option phase/i);
    const visible = stateWire();
    Object.assign(visible.seats[1], { visible_gesture: 'rock' });
    expect(normalizeRPSState(visible).seats[1]).toHaveProperty('visibleGesture', 'rock');
  });

  it('enforces the active/deidentified seat union without identity placeholders', () => {
    const wire = stateWire();
    wire.seats[2] = {
      seat_no: 2,
      viewer: 'opponent',
      deletion_state: 'deidentified',
      starting_balance: '10',
      current_balance: '0',
      current_round_input: '0',
      current_all_in: false,
      total_input: '10',
      total_returned: '0',
      timeout_count: '1',
    } as ReturnType<typeof seat>;
    const parsed = normalizeRPSState(wire);
    expect(parsed.seats[2].deletionState).toBe('deidentified');
    expect(parsed.seats[2]).not.toHaveProperty('displayName');
    Object.assign(wire.seats[2], { visible_gesture: 'paper' });
    expect(() => normalizeRPSState(wire)).toThrow(/seat 2/i);
  });

  it('enforces self/opponent fun visibility, seat money, terminal money, and reminder matrices', () => {
    const selfHidden = stateWire();
    selfHidden.seats[0].fun_snapshot = { state: 'none' };
    expect(() => normalizeRPSState(selfHidden)).toThrow(/self state/i);

    const opponentEarlyFull = stateWire();
    opponentEarlyFull.seats[1].fun_snapshot = {
      state: 'full',
      completed_count: '9',
      profitable_count: '4',
      rock_count: '3',
      scissors_count: '3',
      paper_count: '3',
    };
    expect(() => normalizeRPSState(opponentEarlyFull)).toThrow(/full count/i);

    const badMoney = stateWire();
    badMoney.seats[2].current_balance = '8';
    expect(() => normalizeRPSState(badMoney)).toThrow(/money arithmetic/i);

    const badReminder = stateWire('free_pool_gesture');
    badReminder.round_summary.reminder_active = true;
    expect(() => normalizeRPSState(badReminder)).toThrow(/reminder matrix/i);
  });

  it('keeps table money U128 while allowing only lifetime input and return to use U256', () => {
    const overflow = stateWire();
    overflow.seats[0].starting_balance = '340282366920938463463374607431768212';
    expect(() => normalizeRPSState(overflow)).toThrow(/starting balance/i);

    const lifetime = stateWire();
    const aboveU128 = '340282366920938463463374607431768212';
    for (const value of lifetime.seats) {
      value.total_input = aboveU128;
      value.total_returned = (BigInt(aboveU128) - 1n).toString();
    }
    expect(normalizeRPSState(lifetime).seats[0]).toMatchObject({
      totalInput: aboveU128,
      totalReturned: (BigInt(aboveU128) - 1n).toString(),
    });
  });

  it('replaces an identity epoch wholesale and rejects old epoch/session/pending regressions', () => {
    const current = normalizeRPSHome({ kind: 'session', session: stateWire('gesture', '7', '2') });
    const older = normalizeRPSHome({ kind: 'session', session: stateWire('gesture', '99', '1') });
    const newerEpoch = normalizeRPSHome({
      kind: 'session',
      session: stateWire('gesture', '1', '3'),
    });
    const newerRevision = normalizeRPSHome({
      kind: 'session',
      session: stateWire('gesture', '8', '2'),
    });
    expect(shouldApplyRPSReplacement(current, older)).toBe(false);
    expect(shouldApplyRPSReplacement(current, newerEpoch)).toBe(true);
    expect(shouldApplyRPSReplacement(current, newerRevision)).toBe(true);
    const pending = pendingHome();
    expect(shouldApplyRPSReplacement(pending, current)).toBe(false);
    expect(shouldApplyRPSReplacement(current, pending)).toBe(true);

    const idle = normalizeRPSHome({
      kind: 'idle',
      tutorial_seen: true,
      modes: {
        quick: {
          enabled: true,
          base: '1',
          pumps_bp: { platform: 100, welfare: 50, thursday: 0 },
          queue_seconds: 60,
          gesture_seconds: 10,
          dealer_seconds: 8,
          follower_seconds: 8,
          queue_capacity: 4096,
        },
        standard: {
          enabled: true,
          base: '2',
          pumps_bp: { platform: 100, welfare: 50, thursday: 0 },
          queue_seconds: 60,
          gesture_seconds: 10,
          dealer_seconds: 8,
          follower_seconds: 8,
          queue_capacity: 4096,
        },
        deathmatch: {
          enabled: true,
          base: '3',
          pumps_bp: { platform: 100, welfare: 50, thursday: 0 },
          queue_seconds: 60,
          gesture_seconds: 10,
          dealer_seconds: 8,
          follower_seconds: 8,
          queue_capacity: 4096,
        },
      },
    });
    const queued = normalizeRPSHome({
      kind: 'queue',
      queue: {
        id: 'rpsq_AAAAAAAAAAAAAAAAAAAAAA',
        mode: 'quick',
        state: 'waiting',
        revision: '1',
        deadline: 1_800_000_010,
        server_now: 1_800_000_000,
      },
    });
    expect(shouldApplyRPSReplacement(queued, idle)).toBe(true);
    expect(shouldApplyRPSReplacement(pending, idle)).toBe(true);
  });

  it('validates selected-mode 30-day/min10 leaderboard arithmetic', () => {
    const board = normalizeRPSLeaderboard(
      {
        mode: 'quick',
        board: 'profit_rate',
        window_days: 30,
        window_start: 1_700_000_000,
        min_sessions: 10,
        rows: [
          {
            rank: '1',
            identity: { kind: 'anonymous' },
            session_count: '10',
            profitable_count: '5',
            profit_rate: '50',
            is_me: false,
          },
        ],
        me: null,
      },
      'quick',
      'profit_rate',
    );
    expect(board.rows[0]).toMatchObject({ board: 'profit_rate', profitRate: '50' });
    expect(() =>
      normalizeRPSLeaderboard(
        {
          mode: 'quick',
          board: 'profit_rate',
          window_days: 30,
          window_start: 1_700_000_000,
          min_sessions: 10,
          rows: [
            {
              rank: '1',
              identity: { kind: 'anonymous' },
              session_count: '10',
              profitable_count: '5',
              profit_rate: '50.01',
              is_me: false,
            },
          ],
          me: null,
        },
        'quick',
        'profit_rate',
      ),
    ).toThrow(/arithmetic/i);
  });

  it('accepts disabled zero-base modes but rejects zero base when enabled', () => {
    const mode = {
      enabled: false,
      base: '0',
      pumps_bp: { platform: 100, welfare: 50, thursday: 0 },
      queue_seconds: 60,
      gesture_seconds: 10,
      dealer_seconds: 8,
      follower_seconds: 8,
      queue_capacity: 4096,
    };
    const idle = {
      kind: 'idle',
      tutorial_seen: false,
      modes: { quick: mode, standard: mode, deathmatch: mode },
    };
    expect(normalizeRPSHome(idle)).toMatchObject({ kind: 'idle', modes: { quick: { base: '0' } } });
    mode.queue_capacity = 4_095;
    expect(() => normalizeRPSHome(idle)).toThrow(/capacity/i);
    mode.queue_capacity = 4_096;
    mode.enabled = true;
    expect(() => normalizeRPSHome(idle)).toThrow(/enabled base/i);
  });

  it('requires the private pending-result outcome to match the own wallet-net sign', () => {
    const pending = {
      kind: 'pending_result',
      result: {
        session_id: sessionID,
        mode: 'quick',
        terminal_reason: 'quick_resolved',
        own_seat_no: 0,
        own_input: '1',
        own_returned: '0',
        own_wallet_net: '-1',
        seats: [
          { seat_no: 0, result: 'win' },
          { seat_no: 1, result: 'loss' },
          { seat_no: 2, result: 'tie' },
        ],
        created_at: 1_800_000_000,
      },
    };
    expect(() => normalizeRPSHome(pending)).toThrow(/own result/i);
    pending.result.seats[0].result = 'loss';
    pending.result.own_wallet_net = '-2';
    expect(() => normalizeRPSHome(pending)).toThrow(/result arithmetic/i);
  });
});
