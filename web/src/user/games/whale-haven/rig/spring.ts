import type { SpringPhysicsConfig } from './types';

export interface SpringState {
  angleDeg: number;
  velocityDegPerSec: number;
}

export function createSpringState(initialDeg = 0): SpringState {
  return {
    angleDeg: initialDeg,
    velocityDegPerSec: 0,
  };
}

/**
 * 二阶阻尼弹簧积分器（半隐式欧拉法，适配水下柔顺惯性跟随）
 */
export function stepSpring(
  state: SpringState,
  targetDeg: number,
  config: SpringPhysicsConfig,
  dtSec: number,
  externalImpulseDegPerSec2 = 0,
): void {
  const dt = Math.min(Math.max(dtSec, 0.001), 0.045);
  const displacement = state.angleDeg - targetDeg;
  const accel =
    -config.stiffness * displacement -
    config.damping * state.velocityDegPerSec +
    externalImpulseDegPerSec2;

  state.velocityDegPerSec += accel * dt;
  state.angleDeg += state.velocityDegPerSec * dt;

  // 软边界限幅，避免呆毛、卷发与大尾巴在剧烈戳动时穿模折断
  if (state.angleDeg > config.maxAngleDeg) {
    state.angleDeg = config.maxAngleDeg;
    if (state.velocityDegPerSec > 0) {
      state.velocityDegPerSec *= -0.25;
    }
  } else if (state.angleDeg < -config.maxAngleDeg) {
    state.angleDeg = -config.maxAngleDeg;
    if (state.velocityDegPerSec < 0) {
      state.velocityDegPerSec *= -0.25;
    }
  }
}

export function addSpringImpulse(state: SpringState, deltaVelocityDegPerSec: number): void {
  state.velocityDegPerSec += deltaVelocityDegPerSec;
}
