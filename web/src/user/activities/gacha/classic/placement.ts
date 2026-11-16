import type { Point, RigidMotion } from './types';

export type Placement = { angle: number; translation: Point };

const identity: Placement = { angle: 0, translation: [0, 0] };

function turn([x, y]: Point, angle: number): Point {
  const sine = Math.sin(angle);
  const cosine = Math.cos(angle);
  return [x * cosine - y * sine, x * sine + y * cosine];
}

export function placementAt(motion: RigidMotion, time: number): Placement {
  const parent = motion.parent ? placementAt(motion.parent, time) : identity;
  const phase = (time * 2 * Math.PI) / motion.period + motion.delay;
  const angle = Math.sin(phase) * motion.rotation;
  const [px, py] = motion.pivot;
  const [rx, ry] = turn(motion.pivot, angle);
  const [dx, dy] = turn(
    [
      px - rx + Math.sin(phase) * motion.amplitude[0],
      py - ry + Math.sin(phase + 0.32) * motion.amplitude[1],
    ],
    parent.angle,
  );
  return {
    angle: angle + parent.angle,
    translation: [dx + parent.translation[0], dy + parent.translation[1]],
  };
}
