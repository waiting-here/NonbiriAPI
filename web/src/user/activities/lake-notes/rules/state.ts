import type { Cast, Profile } from './types';
import { RULES_ID } from './catalog';

export function stateBytes(p: Profile, c: Cast) {
  const floats = [
    p.clockMinutes,
    c.waitRemaining,
    c.barY,
    c.barVelocity,
    c.progress,
    c.elapsed,
    c.hitTime,
    c.effectiveTime,
    c.currentMissTime,
    c.longestMissTime,
    ...(c.fish
      ? [c.fish.position, c.fish.y, c.fish.speed, c.fish.target, c.fish.drift]
      : [0, 0, 0, 0, 0]),
    c.treasure?.y || 0,
    c.treasure?.progress || 0,
  ];
  const phase =
    ({ waiting: 1, playing: 2, success: 3, failed: 4 } as Record<string, number>)[c.phase] || 0;
  const flags =
    (c.wasHit ? 1 : 0) |
    (c.held ? 2 : 0) |
    (c.paused ? 4 : 0) |
    (c.fish ? 8 : 0) |
    (c.treasure ? 16 | (c.treasure.secured ? 32 : 0) : 0) |
    (c.result?.perfect ? 64 : 0);
  const ints = [p.day, c.tick, c.motion.state, c.motion.draw_count, phase, flags];
  const bytes = new Uint8Array((floats.length + ints.length) * 8),
    view = new DataView(bytes.buffer);
  floats.forEach((v, i) => view.setFloat64(i * 8, v, true));
  ints.forEach((v, i) => view.setBigUint64((floats.length + i) * 8, BigInt(v), true));
  return bytes;
}
/** Float state validation is performed before any prediction or restoration. */
export function validateCast(c: Cast) {
  if (
    c.rules_id !== RULES_ID ||
    !Number.isInteger(c.motion.state) ||
    c.motion.state <= 0 ||
    c.motion.state > 4294967295 ||
    !Number.isSafeInteger(c.tick) ||
    c.tick < 0
  )
    throw Error('invalid cast identity');
  const visit = (value: unknown): void => {
    if (typeof value === 'number' && !Number.isFinite(value)) throw Error('nonfinite state');
    if (typeof value === 'object' && value !== null) Object.values(value).forEach(visit);
  };
  visit(c);
  if (
    !['waiting', 'playing', 'success', 'failed'].includes(c.phase) ||
    c.progress < 0 ||
    c.progress > 1 ||
    c.barY < 0 ||
    c.barY > 1 ||
    c.snapshot.barHeight < 0.08 ||
    c.snapshot.barHeight > 0.5 ||
    (c.fish &&
      (c.fish.position < 0 || c.fish.position > 532 || c.fish.y !== c.fish.position / 568)) ||
    (c.treasure && (c.treasure.progress < 0 || c.treasure.progress > 1))
  )
    throw Error('invalid cast bounds');
  return c;
}
