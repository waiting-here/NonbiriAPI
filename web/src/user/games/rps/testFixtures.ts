import type { RPSPhase } from './types';

export const rpsTestSessionID = 'rps_AAAAAAAAAAAAAAAAAAAAAA';

export function rpsSeatWire(seatNo: number) {
  return {
    seat_no: seatNo,
    viewer: seatNo === 0 ? 'self' : 'opponent',
    deletion_state: 'active',
    display_name: `Player ${seatNo + 1}`,
    avatar_url: null,
    starting_balance: '10',
    current_balance: '9',
    current_round_input: '1',
    current_all_in: false,
    total_input: '1',
    total_returned: '0',
    timeout_count: '0',
    fun_snapshot:
      seatNo === 0
        ? {
            state: 'full',
            completed_count: '0',
            profitable_count: '0',
            rock_count: '0',
            scissors_count: '0',
            paper_count: '0',
          }
        : { state: 'none' },
  };
}

export function rpsStateWire(phase: RPSPhase = 'gesture', revision = '1', epoch = '1') {
  const pool = phase === 'paid_pool_gesture' || phase === 'free_pool_gesture';
  const terminal = phase === 'terminal_processing';
  const option =
    phase === 'gesture' || pool || phase === 'ultimate_gesture'
      ? ['gesture']
      : phase === 'dealer_raise'
        ? ['dealer_decision']
        : phase === 'followers'
          ? ['follower_decision']
          : [];
  return {
    session_id: rpsTestSessionID,
    mode: 'standard',
    state: terminal ? 'terminal_processing' : 'started',
    phase,
    phase_seq: '1',
    revision,
    identity_epoch: epoch,
    server_now: 1_800_000_000,
    deadline: terminal ? null : 1_800_000_010,
    rule_snapshot: {
      rules_version: 1,
      base: '1',
      pumps_bp: { platform: 100, welfare: 50, thursday: 0 },
      gesture_seconds: 10,
      dealer_seconds: 8,
      follower_seconds: 8,
      standard_multiplier: 5,
      free_tie_reminder: 3,
      free_tie_limit: 6,
    },
    economy: {
      player_pool: '3',
      permanent_multiplier: '1',
      pool_base_multiplier: pool ? '1' : null,
      current_plan_multiplier: pool || terminal ? null : '1',
      dealer_raise: phase === 'followers' ? '1' : null,
      cuts: { platform: '0', welfare: '0', thursday: '0' },
      welfare_carry: '0',
    },
    seats: [rpsSeatWire(0), rpsSeatWire(1), rpsSeatWire(2)].map((seat) =>
      terminal ? { ...seat, terminal_return: '9', wallet_net: '-1' } : seat,
    ),
    current_actor_options: option,
    round_summary: {
      base_round_count: '1',
      paid_tie_count: '0',
      free_tie_count: '0',
      paid_pool_streak: '0',
      free_pool_streak: '0',
      reminder_active: false,
      last_reveal_result: null,
    },
    recent_events: [],
    events_truncated: false,
    first_available_seq: '0',
  };
}
