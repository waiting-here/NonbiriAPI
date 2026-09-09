import type { GameSoundCue } from '../common/sound';
import type { RPSHomeState } from './types';

// Called only for an accepted live replacement; recovery snapshots stay silent.
export function rpsReplacementCue(
  previous: RPSHomeState | undefined,
  next: RPSHomeState,
): GameSoundCue | null {
  if (next.kind === 'pending_result') {
    if (previous?.kind !== 'session' || previous.session.sessionID !== next.result.sessionID)
      return null;
    const own = next.result.seats.find((seat) => seat.seatNo === next.result.ownSeatNo);
    return own && own.result !== 'deidentified' ? own.result : 'end';
  }
  if (next.kind !== 'session' || next.session.state === 'terminal_processing') return null;
  if (previous?.kind === 'queue') return 'phase';
  if (
    previous?.kind !== 'session' ||
    previous.session.sessionID !== next.session.sessionID ||
    previous.session.identityEpoch !== next.session.identityEpoch
  )
    return null;
  const lastSeq = previous.session.recentEvents.reduce(
    (maximum, event) => (BigInt(event.seq) > maximum ? BigInt(event.seq) : maximum),
    0n,
  );
  const reveal = [...next.session.recentEvents]
    .reverse()
    .find(
      (event) =>
        event.kind === 'reveal' &&
        BigInt(event.seq) > lastSeq &&
        event.identityEpoch === next.session.identityEpoch &&
        BigInt(event.phaseSeq) >= BigInt(previous.session.phaseSeq),
    );
  if (
    reveal &&
    ['three_equal', 'all_distinct', 'one_surrender_tie'].includes(
      String(reveal.safePayload.result_code),
    )
  )
    return 'tie';
  if (BigInt(next.session.phaseSeq) <= BigInt(previous.session.phaseSeq)) return null;
  return next.session.currentActorOptions[0] === 'follower_decision' ? 'follow' : 'phase';
}
