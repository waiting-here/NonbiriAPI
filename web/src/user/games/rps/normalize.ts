import { RPS_MODES, type RPSMode, type RPSModeConfig } from '../common/types';
import {
  booleanValue,
  creditsToMilli,
  creditsValue,
  decimalValue,
  enumValue,
  exactRecord,
  httpsURL,
  invalidResponse,
  opaqueID,
  publicIdentity,
  safeInteger,
  textValue,
  unixTime,
} from '../common/strict';
import {
  GESTURES,
  RPS_PHASES,
  type ActiveRPSSeat,
  type DeletedRPSSeat,
  type FunSnapshot,
  type RPSHomeState,
  type RPSLeaderboard,
  type RPSLeaderboardRow,
  type RPSPendingResult,
  type RPSQueue,
  type RPSRecentEvent,
  type RPSSeat,
  type RPSState,
} from './types';

const ACTIONS = ['gesture', 'dealer_decision', 'follower_decision'] as const;
const REVEALS = [
  'three_equal',
  'all_distinct',
  'one_beats_two',
  'two_beat_one',
  'one_surrender_tie',
  'one_surrender_decided',
  'two_surrenders',
] as const;
const EVENT_KINDS = [
  'phase_changed',
  'action_locked',
  'reveal',
  'settlement',
  'reminder',
  'identity_reset',
  'terminal',
] as const;
const TERMINAL_REASONS = [
  'quick_resolved',
  'standard_round_limit',
  'standard_insufficient_balance',
  'deathmatch_balance_exhausted',
  'ultimate_resolved',
  'free_tie_limit',
] as const;

function mode(value: unknown, field: string): RPSMode {
  return enumValue(value, RPS_MODES, field);
}

function normalizePumps(value: unknown, field: string) {
  const record = exactRecord(value, ['platform', 'welfare', 'thursday'], [], field);
  const result = {
    platform: safeInteger(record.platform, 0, 9_999, `${field} platform`),
    welfare: safeInteger(record.welfare, 0, 9_999, `${field} welfare`),
    thursday: safeInteger(record.thursday, 0, 9_999, `${field} Thursday`),
  };
  if (result.platform + result.welfare + result.thursday >= 10_000)
    invalidResponse(`${field} total`);
  return result;
}

function normalizeModeConfig(value: unknown, field: string): RPSModeConfig {
  const record = exactRecord(
    value,
    [
      'enabled',
      'base',
      'pumps_bp',
      'queue_seconds',
      'gesture_seconds',
      'dealer_seconds',
      'follower_seconds',
      'queue_capacity',
    ],
    [],
    field,
  );
  const enabled = booleanValue(record.enabled, `${field} enabled`);
  const base = creditsValue(record.base, {}, `${field} base`);
  if (enabled && base === '0') invalidResponse(`${field} enabled base`);
  return {
    enabled,
    base,
    pumpsBP: normalizePumps(record.pumps_bp, `${field} pumps`),
    queueSeconds: safeInteger(record.queue_seconds, 30, 120, `${field} queue seconds`),
    gestureSeconds: safeInteger(record.gesture_seconds, 5, 20, `${field} gesture seconds`),
    dealerSeconds: safeInteger(record.dealer_seconds, 5, 15, `${field} dealer seconds`),
    followerSeconds: safeInteger(record.follower_seconds, 5, 15, `${field} follower seconds`),
    queueCapacity: safeInteger(record.queue_capacity, 4_096, 4_096, `${field} capacity`),
  };
}

export function normalizeRPSQueue(value: unknown): RPSQueue {
  const record = exactRecord(
    value,
    ['id', 'mode', 'state', 'revision', 'deadline', 'server_now'],
    [],
    'RPS queue',
  );
  if (record.state !== 'waiting') invalidResponse('RPS queue state');
  const deadline = unixTime(record.deadline, 'RPS queue deadline');
  const serverNow = unixTime(record.server_now, 'RPS queue server time');
  return {
    id: opaqueID(record.id, 'rpsq_', 'RPS queue id'),
    mode: mode(record.mode, 'RPS queue mode'),
    state: 'waiting',
    revision: decimalValue(record.revision, { bits: 128, positive: true }, 'RPS queue revision'),
    deadline,
    serverNow,
  };
}

function funSnapshot(value: unknown, field: string, viewer: 'self' | 'opponent'): FunSnapshot {
  const discriminator = exactRecord(
    value,
    ['state'],
    ['completed_count', 'profitable_count', 'rock_count', 'scissors_count', 'paper_count'],
    field,
  );
  if (discriminator.state === 'none') {
    if (viewer === 'self') invalidResponse(`${field} self state`);
    exactRecord(value, ['state'], [], field);
    return { state: 'none' };
  }
  if (discriminator.state === 'insufficient') {
    if (viewer === 'self') invalidResponse(`${field} self state`);
    const record = exactRecord(value, ['state', 'completed_count'], [], field);
    const completedCount = decimalValue(
      record.completed_count,
      { bits: 128, positive: true },
      `${field} count`,
    );
    if (BigInt(completedCount) >= 10n) invalidResponse(`${field} insufficient count`);
    return {
      state: 'insufficient',
      completedCount,
    };
  }
  if (discriminator.state !== 'full') invalidResponse(field);
  const record = exactRecord(
    value,
    ['state', 'completed_count', 'profitable_count', 'rock_count', 'scissors_count', 'paper_count'],
    [],
    field,
  );
  const completedCount = decimalValue(record.completed_count, { bits: 128 }, `${field} count`);
  if (viewer === 'opponent' && BigInt(completedCount) < 10n) invalidResponse(`${field} full count`);
  const profitableCount = decimalValue(
    record.profitable_count,
    { bits: 128 },
    `${field} profitable`,
  );
  if (BigInt(profitableCount) > BigInt(completedCount))
    invalidResponse(`${field} profitable count`);
  return {
    state: 'full',
    completedCount,
    profitableCount,
    rockCount: decimalValue(record.rock_count, { bits: 128 }, `${field} rock`),
    scissorsCount: decimalValue(record.scissors_count, { bits: 128 }, `${field} scissors`),
    paperCount: decimalValue(record.paper_count, { bits: 128 }, `${field} paper`),
  };
}

function terminalMoney(
  record: Record<string, unknown>,
  field: string,
): { terminalReturn?: string; walletNet?: string } {
  const hasReturn = Object.prototype.hasOwnProperty.call(record, 'terminal_return');
  const hasNet = Object.prototype.hasOwnProperty.call(record, 'wallet_net');
  if (hasReturn !== hasNet) invalidResponse(`${field} terminal money`);
  return hasReturn
    ? {
        terminalReturn: creditsValue(record.terminal_return, {}, `${field} terminal return`),
        walletNet: creditsValue(record.wallet_net, { signed: true }, `${field} wallet net`),
      }
    : {};
}

function seatMoney(record: Record<string, unknown>, field: string) {
  return {
    seatNo: safeInteger(record.seat_no, 0, 2, `${field} number`),
    startingBalance: creditsValue(record.starting_balance, {}, `${field} starting balance`),
    currentBalance: creditsValue(record.current_balance, {}, `${field} current balance`),
    currentRoundInput: creditsValue(record.current_round_input, {}, `${field} round input`),
    currentAllIn: booleanValue(record.current_all_in, `${field} all-in`),
    totalInput: creditsValue(record.total_input, { bits: 256 }, `${field} total input`),
    totalReturned: creditsValue(record.total_returned, { bits: 256 }, `${field} total returned`),
    timeoutCount: decimalValue(record.timeout_count, { bits: 128 }, `${field} timeout count`),
    ...terminalMoney(record, field),
  };
}

function normalizeSeat(value: unknown, field: string): RPSSeat {
  const discriminator = exactRecord(
    value,
    [
      'seat_no',
      'viewer',
      'deletion_state',
      'starting_balance',
      'current_balance',
      'current_round_input',
      'current_all_in',
      'total_input',
      'total_returned',
      'timeout_count',
    ],
    [
      'display_name',
      'avatar_url',
      'fun_snapshot',
      'visible_gesture',
      'follower_action',
      'terminal_return',
      'wallet_net',
    ],
    field,
  );
  if (discriminator.deletion_state === 'active') {
    const record = exactRecord(
      value,
      [
        'seat_no',
        'viewer',
        'deletion_state',
        'display_name',
        'avatar_url',
        'starting_balance',
        'current_balance',
        'current_round_input',
        'current_all_in',
        'total_input',
        'total_returned',
        'timeout_count',
        'fun_snapshot',
      ],
      ['visible_gesture', 'follower_action', 'terminal_return', 'wallet_net'],
      field,
    );
    const viewer = enumValue(record.viewer, ['self', 'opponent'] as const, `${field} viewer`);
    const seat: ActiveRPSSeat = {
      ...seatMoney(record, field),
      viewer,
      deletionState: 'active',
      displayName: textValue(record.display_name, 128, `${field} display name`),
      avatarURL: httpsURL(record.avatar_url, `${field} avatar`),
      funSnapshot: funSnapshot(record.fun_snapshot, `${field} fun snapshot`, viewer),
      ...(Object.prototype.hasOwnProperty.call(record, 'visible_gesture')
        ? {
            visibleGesture: enumValue(record.visible_gesture, GESTURES, `${field} visible gesture`),
          }
        : {}),
      ...(Object.prototype.hasOwnProperty.call(record, 'follower_action')
        ? {
            followerAction: enumValue(
              record.follower_action,
              ['call', 'surrender'] as const,
              `${field} follower action`,
            ),
          }
        : {}),
    };
    if (seat.followerAction && viewer !== 'self')
      invalidResponse(`${field} private follower action`);
    return seat;
  }
  const record = exactRecord(
    value,
    [
      'seat_no',
      'viewer',
      'deletion_state',
      'starting_balance',
      'current_balance',
      'current_round_input',
      'current_all_in',
      'total_input',
      'total_returned',
      'timeout_count',
    ],
    ['terminal_return', 'wallet_net'],
    field,
  );
  if (record.viewer !== 'opponent') invalidResponse(`${field} deleted viewer`);
  const deletionState = enumValue(
    record.deletion_state,
    ['deletion_pending', 'deidentified'] as const,
    `${field} deletion state`,
  );
  const seat: DeletedRPSSeat = { ...seatMoney(record, field), viewer: 'opponent', deletionState };
  return seat;
}

function eventPayload(
  kind: string,
  value: unknown,
  field: string,
): Readonly<Record<string, unknown>> {
  if (kind === 'phase_changed') {
    const record = exactRecord(value, ['phase', 'deadline'], [], field);
    enumValue(record.phase, RPS_PHASES.slice(0, 6), `${field} phase`);
    unixTime(record.deadline, `${field} deadline`);
    return record;
  }
  if (kind === 'action_locked') {
    const record = exactRecord(value, ['seat_no', 'action_kind'], [], field);
    safeInteger(record.seat_no, 0, 2, `${field} seat`);
    enumValue(record.action_kind, ACTIONS, `${field} action`);
    return record;
  }
  if (kind === 'reveal') {
    const record = exactRecord(value, ['gestures', 'result_code'], [], field);
    if (!Array.isArray(record.gestures) || record.gestures.length !== 3)
      invalidResponse(`${field} gestures`);
    const seen = new Set<number>();
    for (const item of record.gestures) {
      const gesture = exactRecord(item, ['seat_no', 'gesture'], [], `${field} gesture`);
      const seat = safeInteger(gesture.seat_no, 0, 2, `${field} gesture seat`);
      if (seen.has(seat)) invalidResponse(`${field} duplicate seat`);
      seen.add(seat);
      enumValue(gesture.gesture, GESTURES, `${field} gesture`);
    }
    enumValue(record.result_code, REVEALS, `${field} result`);
    return record;
  }
  if (kind === 'settlement') {
    const record = exactRecord(value, ['seat_deltas', 'player_pool', 'cuts'], [], field);
    if (!Array.isArray(record.seat_deltas) || record.seat_deltas.length !== 3)
      invalidResponse(`${field} deltas`);
    const seen = new Set<number>();
    for (const item of record.seat_deltas) {
      const delta = exactRecord(item, ['seat_no', 'delta'], [], `${field} delta`);
      const seat = safeInteger(delta.seat_no, 0, 2, `${field} seat`);
      if (seen.has(seat)) invalidResponse(`${field} duplicate seat`);
      seen.add(seat);
      creditsValue(delta.delta, { signed: true }, `${field} delta`);
    }
    creditsValue(record.player_pool, {}, `${field} pool`);
    const cuts = exactRecord(record.cuts, ['platform', 'welfare', 'thursday'], [], `${field} cuts`);
    for (const key of ['platform', 'welfare', 'thursday'] as const)
      creditsValue(cuts[key], {}, `${field} ${key}`);
    return record;
  }
  if (kind === 'reminder') {
    const record = exactRecord(value, ['free_tie_count'], [], field);
    const count = decimalValue(record.free_tie_count, { bits: 128 }, `${field} count`);
    if (BigInt(count) < 3n || BigInt(count) > 5n) invalidResponse(`${field} count`);
    return record;
  }
  if (kind === 'identity_reset') {
    const record = exactRecord(value, ['seat_no', 'identity_epoch'], [], field);
    safeInteger(record.seat_no, 0, 2, `${field} seat`);
    decimalValue(record.identity_epoch, { bits: 128, positive: true }, `${field} epoch`);
    return record;
  }
  if (kind === 'terminal') {
    const record = exactRecord(value, ['terminal_reason'], [], field);
    enumValue(record.terminal_reason, TERMINAL_REASONS, `${field} reason`);
    return record;
  }
  invalidResponse(field);
}

function recentEvent(value: unknown, field: string): RPSRecentEvent {
  const record = exactRecord(
    value,
    ['seq', 'identity_epoch', 'kind', 'phase_seq', 'safe_payload'],
    [],
    field,
  );
  const kind = enumValue(record.kind, EVENT_KINDS, `${field} kind`);
  return {
    seq: decimalValue(record.seq, { bits: 128, positive: true }, `${field} sequence`),
    identityEpoch: decimalValue(
      record.identity_epoch,
      { bits: 128, positive: true },
      `${field} epoch`,
    ),
    kind,
    phaseSeq: decimalValue(
      record.phase_seq,
      { bits: 128, positive: true },
      `${field} phase sequence`,
    ),
    safePayload: eventPayload(kind, record.safe_payload, `${field} payload`),
  };
}

export function normalizeRPSState(value: unknown): RPSState {
  const record = exactRecord(
    value,
    [
      'session_id',
      'mode',
      'state',
      'phase',
      'phase_seq',
      'revision',
      'identity_epoch',
      'server_now',
      'deadline',
      'rule_snapshot',
      'economy',
      'seats',
      'current_actor_options',
      'round_summary',
      'recent_events',
      'events_truncated',
      'first_available_seq',
    ],
    [],
    'RPS state',
  );
  const state = enumValue(
    record.state,
    ['started', 'terminal_processing'] as const,
    'RPS state kind',
  );
  const phase = enumValue(record.phase, RPS_PHASES, 'RPS phase');
  const deadline = record.deadline === null ? null : unixTime(record.deadline, 'RPS deadline');
  if (
    (state === 'started' && (phase === 'terminal_processing' || deadline === null)) ||
    (state === 'terminal_processing' && (phase !== 'terminal_processing' || deadline !== null))
  )
    invalidResponse('RPS state/phase matrix');
  const rules = exactRecord(
    record.rule_snapshot,
    [
      'rules_version',
      'base',
      'pumps_bp',
      'gesture_seconds',
      'dealer_seconds',
      'follower_seconds',
      'standard_multiplier',
      'free_tie_reminder',
      'free_tie_limit',
    ],
    [],
    'RPS rule snapshot',
  );
  const ruleSnapshot = {
    rulesVersion: safeInteger(rules.rules_version, 1, Number.MAX_SAFE_INTEGER, 'RPS rules version'),
    base: creditsValue(rules.base, { positive: true }, 'RPS base'),
    pumpsBP: normalizePumps(rules.pumps_bp, 'RPS pumps'),
    gestureSeconds: safeInteger(rules.gesture_seconds, 5, 20, 'RPS gesture seconds'),
    dealerSeconds: safeInteger(rules.dealer_seconds, 5, 15, 'RPS dealer seconds'),
    followerSeconds: safeInteger(rules.follower_seconds, 5, 15, 'RPS follower seconds'),
    standardMultiplier: safeInteger(
      rules.standard_multiplier,
      5,
      5,
      'RPS standard multiplier',
    ) as 5,
    freeTieReminder: safeInteger(rules.free_tie_reminder, 3, 3, 'RPS reminder') as 3,
    freeTieLimit: safeInteger(rules.free_tie_limit, 6, 6, 'RPS tie limit') as 6,
  };
  const economyRecord = exactRecord(
    record.economy,
    [
      'player_pool',
      'permanent_multiplier',
      'pool_base_multiplier',
      'current_plan_multiplier',
      'dealer_raise',
      'cuts',
      'welfare_carry',
    ],
    [],
    'RPS economy',
  );
  const nullableDecimal = (entry: unknown, field: string) =>
    entry === null ? null : decimalValue(entry, { bits: 128, positive: true }, field);
  const poolBaseMultiplier = nullableDecimal(
    economyRecord.pool_base_multiplier,
    'RPS pool multiplier',
  );
  const currentPlanMultiplier = nullableDecimal(
    economyRecord.current_plan_multiplier,
    'RPS plan multiplier',
  );
  const dealerRaise =
    economyRecord.dealer_raise === null
      ? null
      : creditsValue(economyRecord.dealer_raise, { positive: true }, 'RPS dealer raise');
  if (
    (['gesture', 'dealer_raise', 'followers', 'ultimate_gesture'].includes(phase) &&
      (currentPlanMultiplier === null || poolBaseMultiplier !== null)) ||
    (['paid_pool_gesture', 'free_pool_gesture'].includes(phase) &&
      (poolBaseMultiplier === null || currentPlanMultiplier !== null)) ||
    (phase === 'terminal_processing' &&
      (poolBaseMultiplier !== null || currentPlanMultiplier !== null)) ||
    (phase !== 'followers' && dealerRaise !== null)
  )
    invalidResponse('RPS economy phase matrix');
  const cutsRecord = exactRecord(
    economyRecord.cuts,
    ['platform', 'welfare', 'thursday'],
    [],
    'RPS cuts',
  );
  const economy = {
    playerPool: creditsValue(economyRecord.player_pool, {}, 'RPS player pool'),
    permanentMultiplier: decimalValue(
      economyRecord.permanent_multiplier,
      { bits: 128, positive: true },
      'RPS permanent multiplier',
    ),
    poolBaseMultiplier,
    currentPlanMultiplier,
    dealerRaise,
    cuts: {
      platform: creditsValue(cutsRecord.platform, {}, 'RPS platform cut'),
      welfare: creditsValue(cutsRecord.welfare, {}, 'RPS welfare cut'),
      thursday: creditsValue(cutsRecord.thursday, {}, 'RPS Thursday cut'),
    },
    welfareCarry: creditsValue(economyRecord.welfare_carry, {}, 'RPS welfare carry'),
  };
  if (!Array.isArray(record.seats) || record.seats.length !== 3) invalidResponse('RPS seats');
  const seats = record.seats.map((seat, index) => normalizeSeat(seat, `RPS seat ${index}`)) as [
    RPSSeat,
    RPSSeat,
    RPSSeat,
  ];
  if (
    seats.some((seat, index) => seat.seatNo !== index) ||
    seats.filter((seat) => seat.deletionState === 'active' && seat.viewer === 'self').length !== 1
  )
    invalidResponse('RPS seat topology');
  for (const seat of seats) {
    const starting = creditsToMilli(seat.startingBalance);
    const current = creditsToMilli(seat.currentBalance);
    const roundInput = creditsToMilli(seat.currentRoundInput);
    const totalInput = creditsToMilli(seat.totalInput, false, 256);
    const totalReturned = creditsToMilli(seat.totalReturned, false, 256);
    if (roundInput > totalInput || starting - totalInput + totalReturned !== current)
      invalidResponse(`RPS seat ${seat.seatNo} money arithmetic`);
    const hasTerminal = seat.terminalReturn !== undefined;
    if ((state === 'terminal_processing') !== hasTerminal)
      invalidResponse(`RPS seat ${seat.seatNo} terminal matrix`);
    if (
      hasTerminal &&
      (creditsToMilli(seat.terminalReturn!) !== current ||
        creditsToMilli(seat.walletNet!, true) !== current - starting)
    )
      invalidResponse(`RPS seat ${seat.seatNo} terminal arithmetic`);
  }
  if (!Array.isArray(record.current_actor_options) || record.current_actor_options.length > 1)
    invalidResponse('RPS actor options');
  const currentActorOptions =
    record.current_actor_options.length === 0
      ? ([] as const)
      : ([enumValue(record.current_actor_options[0], ACTIONS, 'RPS actor option')] as const);
  if (state === 'terminal_processing' && currentActorOptions.length !== 0)
    invalidResponse('RPS terminal actor options');
  const actorOption = currentActorOptions[0];
  if (
    (actorOption === 'gesture' &&
      !['gesture', 'paid_pool_gesture', 'free_pool_gesture', 'ultimate_gesture'].includes(phase)) ||
    (actorOption === 'dealer_decision' && phase !== 'dealer_raise') ||
    (actorOption === 'follower_decision' && phase !== 'followers')
  )
    invalidResponse('RPS actor option phase');
  const round = exactRecord(
    record.round_summary,
    [
      'base_round_count',
      'paid_tie_count',
      'free_tie_count',
      'paid_pool_streak',
      'free_pool_streak',
      'reminder_active',
      'last_reveal_result',
    ],
    [],
    'RPS round summary',
  );
  const roundSummary = {
    baseRoundCount: decimalValue(round.base_round_count, { bits: 128 }, 'RPS base rounds'),
    paidTieCount: decimalValue(round.paid_tie_count, { bits: 128 }, 'RPS paid ties'),
    freeTieCount: decimalValue(round.free_tie_count, { bits: 128 }, 'RPS free ties'),
    paidPoolStreak: decimalValue(round.paid_pool_streak, { bits: 128 }, 'RPS paid streak'),
    freePoolStreak: decimalValue(round.free_pool_streak, { bits: 128 }, 'RPS free streak'),
    reminderActive: booleanValue(round.reminder_active, 'RPS reminder'),
    lastRevealResult:
      round.last_reveal_result === null
        ? null
        : enumValue(round.last_reveal_result, REVEALS, 'RPS reveal result'),
  };
  const reminderStreak = BigInt(roundSummary.freePoolStreak);
  if (
    roundSummary.reminderActive !==
    (phase === 'free_pool_gesture' && reminderStreak >= 3n && reminderStreak <= 5n)
  )
    invalidResponse('RPS reminder matrix');
  if (!Array.isArray(record.recent_events) || record.recent_events.length > 64)
    invalidResponse('RPS recent events');
  const recentEvents = record.recent_events.map((event, index) =>
    recentEvent(event, `RPS event ${index}`),
  );
  for (let index = 1; index < recentEvents.length; index += 1)
    if (BigInt(recentEvents[index].seq) <= BigInt(recentEvents[index - 1].seq))
      invalidResponse('RPS event order');
  const identityEpoch = decimalValue(
    record.identity_epoch,
    { bits: 128, positive: true },
    'RPS identity epoch',
  );
  if (recentEvents.some((event) => event.identityEpoch !== identityEpoch))
    invalidResponse('RPS event identity epoch');
  const firstAvailableSeq = decimalValue(
    record.first_available_seq,
    { bits: 128 },
    'RPS first event sequence',
  );
  const eventsTruncated = booleanValue(record.events_truncated, 'RPS events truncated');
  if (recentEvents.length > 0) {
    if (
      firstAvailableSeq !== recentEvents[0].seq ||
      eventsTruncated !== BigInt(firstAvailableSeq) > 1n
    )
      invalidResponse('RPS first event sequence');
  } else if (
    (eventsTruncated && BigInt(firstAvailableSeq) === 0n) ||
    (!eventsTruncated && firstAvailableSeq !== '0')
  ) {
    invalidResponse('RPS empty event sequence');
  }
  return {
    sessionID: opaqueID(record.session_id, 'rps_', 'RPS session id'),
    mode: mode(record.mode, 'RPS mode'),
    state,
    phase,
    phaseSeq: decimalValue(record.phase_seq, { bits: 128, positive: true }, 'RPS phase sequence'),
    revision: decimalValue(record.revision, { bits: 128, positive: true }, 'RPS revision'),
    identityEpoch,
    serverNow: unixTime(record.server_now, 'RPS server time'),
    deadline,
    ruleSnapshot,
    economy,
    seats,
    currentActorOptions,
    roundSummary,
    recentEvents,
    eventsTruncated,
    firstAvailableSeq,
  };
}

export function normalizeRPSPending(value: unknown): RPSPendingResult {
  const record = exactRecord(
    value,
    [
      'session_id',
      'mode',
      'terminal_reason',
      'own_seat_no',
      'own_input',
      'own_returned',
      'own_wallet_net',
      'seats',
      'created_at',
    ],
    [],
    'RPS pending result',
  );
  if (!Array.isArray(record.seats) || record.seats.length !== 3)
    invalidResponse('RPS result seats');
  const seats = record.seats.map((item, index) => {
    const seat = exactRecord(item, ['seat_no', 'result'], [], `RPS result seat ${index}`);
    const seatNo = safeInteger(seat.seat_no, index, index, `RPS result seat ${index} number`);
    return {
      seatNo,
      result: enumValue(
        seat.result,
        ['win', 'loss', 'tie', 'deidentified'] as const,
        `RPS result seat ${index}`,
      ),
    };
  });
  const ownSeatNo = safeInteger(record.own_seat_no, 0, 2, 'RPS own seat');
  const ownInput = creditsValue(record.own_input, {}, 'RPS own input');
  const ownReturned = creditsValue(record.own_returned, {}, 'RPS own returned');
  const ownWalletNet = creditsValue(record.own_wallet_net, { signed: true }, 'RPS wallet net');
  const ownSign = creditsToMilli(ownWalletNet, true);
  if (creditsToMilli(ownReturned) - creditsToMilli(ownInput) !== ownSign)
    invalidResponse('RPS own result arithmetic');
  const expectedOwnResult = ownSign > 0n ? 'win' : ownSign < 0n ? 'loss' : 'tie';
  if (seats[ownSeatNo].result !== expectedOwnResult) invalidResponse('RPS own result');
  return {
    sessionID: opaqueID(record.session_id, 'rps_', 'RPS result session'),
    mode: mode(record.mode, 'RPS result mode'),
    terminalReason: enumValue(record.terminal_reason, TERMINAL_REASONS, 'RPS terminal reason'),
    ownSeatNo,
    ownInput,
    ownReturned,
    ownWalletNet,
    seats,
    createdAt: unixTime(record.created_at, 'RPS result time'),
  };
}

export function normalizeRPSHome(value: unknown): RPSHomeState {
  const root = exactRecord(
    value,
    ['kind'],
    ['tutorial_seen', 'modes', 'queue', 'session', 'result'],
    'RPS home',
  );
  if (root.kind === 'idle') {
    const record = exactRecord(value, ['kind', 'tutorial_seen', 'modes'], [], 'RPS idle');
    const modes = exactRecord(record.modes, RPS_MODES, [], 'RPS idle modes');
    return {
      kind: 'idle',
      tutorialSeen: booleanValue(record.tutorial_seen, 'RPS tutorial seen'),
      modes: {
        quick: normalizeModeConfig(modes.quick, 'quick mode'),
        standard: normalizeModeConfig(modes.standard, 'standard mode'),
        deathmatch: normalizeModeConfig(modes.deathmatch, 'deathmatch mode'),
      },
    };
  }
  if (root.kind === 'queue') {
    const record = exactRecord(value, ['kind', 'queue'], [], 'RPS queue home');
    return { kind: 'queue', queue: normalizeRPSQueue(record.queue) };
  }
  if (root.kind === 'session') {
    const record = exactRecord(value, ['kind', 'session'], [], 'RPS session home');
    return { kind: 'session', session: normalizeRPSState(record.session) };
  }
  if (root.kind === 'pending_result') {
    const record = exactRecord(value, ['kind', 'result'], [], 'RPS pending home');
    return { kind: 'pending_result', result: normalizeRPSPending(record.result) };
  }
  invalidResponse('RPS home discriminator');
}

function percentBP(value: unknown, field: string): number {
  if (typeof value !== 'string' || !/^(0|[1-9][0-9]*)(?:\.[0-9]?[1-9])?$/.test(value))
    invalidResponse(field);
  const [whole, fraction = ''] = value.split('.');
  const result = Number(whole) * 100 + Number((fraction + '00').slice(0, 2));
  if (result > 10_000) invalidResponse(field);
  return result;
}
function formatBP(value: bigint): string {
  const whole = value / 100n;
  const fraction = (value % 100n).toString().padStart(2, '0').replace(/0+$/, '');
  return `${whole}${fraction ? `.${fraction}` : ''}`;
}
function leaderboardRow(
  value: unknown,
  board: 'profit_rate' | 'net_profit',
  field: string,
): RPSLeaderboardRow {
  const required =
    board === 'profit_rate'
      ? ['rank', 'identity', 'session_count', 'profitable_count', 'profit_rate', 'is_me']
      : ['rank', 'identity', 'session_count', 'net_profit', 'is_me'];
  const record = exactRecord(value, required, [], field);
  const rank = decimalValue(record.rank, { bits: 256, positive: true }, `${field} rank`);
  const identity = publicIdentity(record.identity, `${field} identity`);
  const sessionCount = decimalValue(
    record.session_count,
    { bits: 128, positive: true },
    `${field} sessions`,
  );
  if (BigInt(sessionCount) < 10n) invalidResponse(`${field} eligibility`);
  const isMe = booleanValue(record.is_me, `${field} me`);
  if (board === 'profit_rate') {
    const profitableCount = decimalValue(
      record.profitable_count,
      { bits: 128 },
      `${field} profitable`,
    );
    if (BigInt(profitableCount) > BigInt(sessionCount)) invalidResponse(`${field} profitable`);
    const profitRate =
      typeof record.profit_rate === 'string'
        ? record.profit_rate
        : invalidResponse(`${field} rate`);
    const bp = percentBP(profitRate, `${field} rate`);
    if (
      formatBP((BigInt(profitableCount) * 10_000n) / BigInt(sessionCount)) !== profitRate ||
      bp > 10_000
    )
      invalidResponse(`${field} rate arithmetic`);
    return { board, rank, identity, sessionCount, profitableCount, profitRate, isMe };
  }
  return {
    board,
    rank,
    identity,
    sessionCount,
    netProfit: creditsValue(record.net_profit, { signed: true }, `${field} net`),
    isMe,
  };
}
export function normalizeRPSLeaderboard(
  value: unknown,
  expectedMode: RPSMode,
  expectedBoard: 'profit_rate' | 'net_profit',
): RPSLeaderboard {
  const record = exactRecord(
    value,
    ['mode', 'board', 'window_days', 'window_start', 'min_sessions', 'rows', 'me'],
    [],
    'RPS leaderboard',
  );
  const parsedMode = mode(record.mode, 'RPS leaderboard mode');
  const board = enumValue(
    record.board,
    ['profit_rate', 'net_profit'] as const,
    'RPS leaderboard board',
  );
  if (
    parsedMode !== expectedMode ||
    board !== expectedBoard ||
    !Array.isArray(record.rows) ||
    record.rows.length > 20
  )
    invalidResponse('RPS leaderboard selection');
  const rows = record.rows.map((row, index) =>
    leaderboardRow(row, board, `RPS leaderboard row ${index}`),
  );
  const me = record.me === null ? null : leaderboardRow(record.me, board, 'RPS leaderboard me');
  if (
    rows.filter((row) => row.isMe).length > 1 ||
    (me && (!me.isMe || rows.some((row) => row.isMe)))
  )
    invalidResponse('RPS leaderboard me');
  return {
    mode: parsedMode,
    board,
    windowDays: safeInteger(record.window_days, 30, 30, 'RPS leaderboard window') as 30,
    windowStart: unixTime(record.window_start, 'RPS leaderboard start'),
    minSessions: safeInteger(record.min_sessions, 10, 10, 'RPS leaderboard minimum') as 10,
    rows,
    me,
  };
}

export function rpsStateEpoch(home: RPSHomeState): string | null {
  return home.kind === 'session' ? home.session.identityEpoch : null;
}
export function rpsStateRevision(home: RPSHomeState): string | null {
  return home.kind === 'session'
    ? home.session.revision
    : home.kind === 'queue'
      ? home.queue.revision
      : null;
}
export function shouldApplyRPSReplacement(
  current: RPSHomeState | undefined,
  next: RPSHomeState,
): boolean {
  if (!current) return true;
  if (current.kind === 'session' && next.kind === 'session') {
    if (current.session.sessionID !== next.session.sessionID) return false;
    const currentEpoch = BigInt(current.session.identityEpoch),
      nextEpoch = BigInt(next.session.identityEpoch);
    return (
      nextEpoch > currentEpoch ||
      (nextEpoch === currentEpoch &&
        BigInt(next.session.revision) > BigInt(current.session.revision))
    );
  }
  if (current.kind === 'session')
    return next.kind === 'pending_result' && next.result.sessionID === current.session.sessionID;
  if (current.kind === 'pending_result')
    return (
      next.kind === 'idle' ||
      (next.kind === 'pending_result' && next.result.sessionID === current.result.sessionID)
    );
  if (current.kind === 'queue') {
    if (next.kind === 'queue')
      return (
        next.queue.id === current.queue.id &&
        BigInt(next.queue.revision) > BigInt(current.queue.revision)
      );
    if (next.kind === 'session') return next.session.mode === current.queue.mode;
    if (next.kind === 'pending_result') return next.result.mode === current.queue.mode;
    return next.kind === 'idle';
  }
  return true;
}
