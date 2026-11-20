import { addSpringImpulse, createSpringState, stepSpring, type SpringState } from './spring';
import type { BoneDefinition, BoneId, SlotId, WhaleRigManifest } from './types';

export type RigActivity = 'idle' | 'eating' | 'swimming';

export interface Matrix2D {
  a: number;
  b: number;
  c: number;
  d: number;
  e: number;
  f: number;
}

export const IDENTITY_MATRIX: Matrix2D = { a: 1, b: 0, c: 0, d: 1, e: 0, f: 0 };

export function multiplyMatrix(m1: Matrix2D, m2: Matrix2D): Matrix2D {
  return {
    a: m1.a * m2.a + m1.c * m2.b,
    b: m1.b * m2.a + m1.d * m2.b,
    c: m1.a * m2.c + m1.c * m2.d,
    d: m1.b * m2.c + m1.d * m2.d,
    e: m1.a * m2.e + m1.c * m2.f + m1.e,
    f: m1.b * m2.e + m1.d * m2.f + m1.f,
  };
}

export function makeBoneLocalMatrix(
  pivotX: number,
  pivotY: number,
  angleDeg: number,
  tx = 0,
  ty = 0,
  sx = 1,
  sy = 1,
): Matrix2D {
  const rad = (angleDeg * Math.PI) / 180;
  const cos = Math.cos(rad);
  const sin = Math.sin(rad);
  // T(pivotX + tx, pivotY + ty) * R(rad) * S(sx, sy) * T(-pivotX, -pivotY)
  const a = cos * sx;
  const b = sin * sx;
  const c = -sin * sy;
  const d = cos * sy;
  const e = pivotX + tx - a * pivotX - c * pivotY;
  const f = pivotY + ty - b * pivotX - d * pivotY;
  return { a, b, c, d, e, f };
}

export interface RigRuntimeState {
  timeSec: number;
  springs: Partial<Record<BoneId, SpringState>>;
  prevHeadAngle: number;
  prevTorsoY: number;
  // Blinking state
  nextBlinkAtSec: number;
  blinkStartSec: number | null;
  // Smooth gaze in [-1, 1]
  gazeX: number;
  gazeY: number;
  // Transient interaction reactions
  headpatUntilSec: number;
  pokedUntilSec: number;
  teacakeUntilSec: number;
}

export function createRigRuntimeState(seedOffsetSec = 0): RigRuntimeState {
  return {
    timeSec: seedOffsetSec,
    springs: {},
    prevHeadAngle: 0,
    prevTorsoY: 0,
    nextBlinkAtSec: seedOffsetSec + 1.8 + Math.random() * 2.4,
    blinkStartSec: null,
    gazeX: 0,
    gazeY: 0,
    headpatUntilSec: -1,
    pokedUntilSec: -1,
    teacakeUntilSec: -1,
  };
}

export function triggerRigImpulse(
  runtime: RigRuntimeState,
  manifest: WhaleRigManifest,
  kind: 'headpat' | 'poke' | 'teacake',
): void {
  for (const bone of manifest.bones) {
    if (!bone.spring) continue;
    if (!runtime.springs[bone.id]) {
      runtime.springs[bone.id] = createSpringState(0);
    }
  }

  if (kind === 'headpat') {
    runtime.headpatUntilSec = runtime.timeSec + 1.9;
    if (runtime.springs.ahoge) addSpringImpulse(runtime.springs.ahoge, -160);
    if (runtime.springs.ear_l) addSpringImpulse(runtime.springs.ear_l, -95);
    if (runtime.springs.ear_r) addSpringImpulse(runtime.springs.ear_r, 95);
    if (runtime.springs.tail_base) addSpringImpulse(runtime.springs.tail_base, 110);
  } else if (kind === 'poke') {
    runtime.pokedUntilSec = runtime.timeSec + 1.1;
    if (runtime.springs.ahoge) addSpringImpulse(runtime.springs.ahoge, 230);
    if (runtime.springs.ear_l) addSpringImpulse(runtime.springs.ear_l, 145);
    if (runtime.springs.ear_r) addSpringImpulse(runtime.springs.ear_r, -145);
    if (runtime.springs.hair_side_l) addSpringImpulse(runtime.springs.hair_side_l, 95);
    if (runtime.springs.hair_side_r) addSpringImpulse(runtime.springs.hair_side_r, -95);
    if (runtime.springs.tail_base) addSpringImpulse(runtime.springs.tail_base, -175);
  } else if (kind === 'teacake') {
    runtime.teacakeUntilSec = runtime.timeSec + 3.6;
    if (runtime.springs.tail_base) addSpringImpulse(runtime.springs.tail_base, 140);
  }
}

export interface EvaluatedRigFrame {
  boneMatrices: Record<BoneId, Matrix2D>;
  slotVariants: Record<SlotId, string>;
  irisOffsetPx: { x: number; y: number };
}

/**
 * 将游戏表情语义映射到 manifest 中的最佳表情预设 ID（自动兼容 V1/V2 manifest）
 */
export function resolveExpressionPresetId(
  manifest: WhaleRigManifest,
  requestedId: string,
  isEating: boolean,
): string {
  const available = new Set(manifest.expressions.map((e) => e.id));

  if (available.has(requestedId)) {
    // 若当前是默认元气态且正在干饭，可优先切换到专用的幸福干饭或保持元气张嘴
    if (requestedId === 'cheerful' && isEating && available.has('happy_eat')) {
      return 'happy_eat';
    }
    return requestedId;
  }

  // 兼容降级映射：
  // 1) 游戏内 'calm' 代表「发呆死鱼眼」，优先找 V2 的 'deadpan'，否则回退 'calm'
  if (requestedId === 'calm') {
    if (available.has('deadpan')) return 'deadpan';
    if (available.has('calm')) return 'calm';
  }
  // 2) 游戏内 'cheerful' 代表「默认元气笑颜」，若旧版 V1 尚未生成 'cheerful'，则回退 'happy_eat' 或首项
  if (requestedId === 'cheerful') {
    if (isEating && available.has('happy_eat')) return 'happy_eat';
    if (available.has('calm')) return 'calm';
  }

  return manifest.expressions[0]?.id ?? 'calm';
}

export function stepAndEvaluateRig(
  runtime: RigRuntimeState,
  manifest: WhaleRigManifest,
  params: {
    dtSec: number;
    outfitId: string;
    expressionKey: string;
    pinnedExpressionId?: string | null;
    activity: RigActivity;
    heldProp?: 'none' | 'silver_fish' | 'berry_cake' | 'pearl';
    pointerNormX: number; // -1..1
    pointerNormY: number; // -1..1
  },
): EvaluatedRigFrame {
  const dt = Math.min(Math.max(params.dtSec, 0.001), 0.05);
  runtime.timeSec += dt;
  const t = runtime.timeSec;

  const isHeadpatting = t < runtime.headpatUntilSec;
  const isPoked = t < runtime.pokedUntilSec;
  const isTeacake = t < runtime.teacakeUntilSec;
  const effectiveEating = params.activity === 'eating' || isTeacake;

  // 1. 解析当前服装与表情预设的插槽变体
  const slotVariants = {} as Record<SlotId, string>;
  for (const slot of manifest.slots) {
    slotVariants[slot.id] = slot.defaultVariant;
  }

  const outfitPreset =
    manifest.outfits.find((o) => o.id === params.outfitId) ?? manifest.outfits[0];
  if (outfitPreset) {
    for (const [slotId, variant] of Object.entries(outfitPreset.slotOverrides)) {
      if (variant) slotVariants[slotId as SlotId] = variant;
    }
  }

  const activeExprId =
    isHeadpatting && manifest.expressions.some((e) => e.id === 'headpat_bliss')
      ? 'headpat_bliss'
      : params.pinnedExpressionId
        ? resolveExpressionPresetId(manifest, params.pinnedExpressionId, false)
        : resolveExpressionPresetId(manifest, params.expressionKey, effectiveEating);

  const exprPreset =
    manifest.expressions.find((e) => e.id === activeExprId) ?? manifest.expressions[0];
  if (exprPreset) {
    for (const [slotId, variant] of Object.entries(exprPreset.slotOverrides)) {
      if (variant) slotVariants[slotId as SlotId] = variant;
    }
  }

  // 手持道具插槽覆盖
  const effectiveProp = isTeacake
    ? 'berry_cake'
    : (params.heldProp ?? (params.activity === 'eating' ? 'silver_fish' : 'none'));
  const propSlot = manifest.slots.find((s) => s.id === 'hand_prop');
  if (propSlot && propSlot.variants[effectiveProp]) {
    slotVariants.hand_prop = effectiveProp;
  }

  // 干饭咀嚼嘴型动态交替（仅在非委屈/非惊慌/非鼓腮锁定状态下生效）
  const mouthSlot = manifest.slots.find((s) => s.id === 'mouth');
  if (
    effectiveEating &&
    !params.pinnedExpressionId &&
    !isHeadpatting &&
    activeExprId !== 'teary' &&
    activeExprId !== 'pout' &&
    activeExprId !== 'panicked' &&
    mouthSlot
  ) {
    const chewPhase = Math.sin(t * 9.5);
    const chewVariant = chewPhase > 0 ? 'chew_open' : 'chew_munched';
    if (mouthSlot.variants[chewVariant]) {
      slotVariants.mouth = chewVariant;
    }
  }

  // 2. 自然随机眨眼（若当前表情本身未闭眼）
  const eyelidsSlot = manifest.slots.find((s) => s.id === 'eyelids');
  const presetAlreadyClosed = slotVariants.eyelids === 'closed';
  if (!presetAlreadyClosed && eyelidsSlot) {
    if (runtime.blinkStartSec === null && t >= runtime.nextBlinkAtSec) {
      runtime.blinkStartSec = t;
    }
    if (runtime.blinkStartSec !== null) {
      const elapsed = t - runtime.blinkStartSec;
      if (elapsed < 0.038) {
        if (eyelidsSlot.variants.half) slotVariants.eyelids = 'half';
      } else if (elapsed < 0.105) {
        if (eyelidsSlot.variants.closed) slotVariants.eyelids = 'closed';
      } else if (elapsed < 0.145) {
        if (eyelidsSlot.variants.half) slotVariants.eyelids = 'half';
      } else {
        runtime.blinkStartSec = null;
        runtime.nextBlinkAtSec = t + 2.5 + Math.random() * 3.1;
      }
    }
  }

  // 3. 视线平滑追踪（发呆死鱼眼或闭眼时视线回正）
  const targetGazeX =
    slotVariants.eyelids === 'closed' || activeExprId === 'deadpan'
      ? 0
      : Math.max(-1, Math.min(1, params.pointerNormX));
  const targetGazeY =
    slotVariants.eyelids === 'closed' || activeExprId === 'deadpan'
      ? 0
      : Math.max(-1, Math.min(1, params.pointerNormY));
  const gazeLerp = Math.min(1, dt * 8.5);
  runtime.gazeX += (targetGazeX - runtime.gazeX) * gazeLerp;
  runtime.gazeY += (targetGazeY - runtime.gazeY) * gazeLerp;

  const irisOffsetPx = {
    x: runtime.gazeX * 4.5,
    y: runtime.gazeY * 2.8,
  };

  // 4. 程序化主骨骼运动（呼吸起伏、摸头摇晃、干饭举手、游泳摆腿）
  const angles: Partial<Record<BoneId, number>> = {};
  const offsets: Partial<Record<BoneId, { x: number; y: number; sx?: number; sy?: number }>> = {};

  const breathWave = Math.sin(t * 2.15);
  const secondaryWave = Math.sin(t * 1.35 + 0.8);

  let torsoY = breathWave * -3.6;
  let torsoSx = 1 + breathWave * 0.008;
  let torsoSy = 1 - breathWave * 0.008;
  let torsoAngle = secondaryWave * 0.8;
  let headAngle = Math.sin(t * 1.65 - 0.5) * 1.8;

  if (isHeadpatting) {
    headAngle += Math.sin(t * 7.2) * 5.2;
    torsoY += Math.abs(Math.sin(t * 7.2)) * 3.2;
  }

  if (isPoked) {
    const rem = Math.max(0, runtime.pokedUntilSec - t) / 1.1;
    const dampOsc = Math.sin((1 - rem) * 26) * rem;
    torsoAngle += dampOsc * 6.5;
    headAngle -= dampOsc * 8.0;
    torsoSx += dampOsc * 0.035;
    torsoSy -= dampOsc * 0.035;
    // 受惊小短手向外微张：x' = px - sin(theta)*L => 左手向外(+theta)，右手向外(-theta)
    angles.arm_l = 16 * rem;
    angles.arm_r = -16 * rem;
  } else if (effectiveEating) {
    // 右手端着小鱼干/茶点向内上方嘴边递送（+theta 向左内收靠近嘴部），左手开心轻晃
    const feedLift = (Math.sin(t * 4.6) + 1) * 0.5; // 0..1
    angles.arm_r = 14 + feedLift * 18;
    angles.arm_l = -4 - Math.sin(t * 4.6 + 1.2) * 5;
    headAngle += Math.sin(t * 4.6) * 2.2;
    torsoY += Math.sin(t * 4.6 * 2) * -2.0;
  } else if (params.activity === 'swimming') {
    torsoAngle = Math.sin(t * 3.4) * 4.2;
    torsoY = Math.sin(t * 3.4) * -8.5;
    headAngle = Math.sin(t * 3.4 - 0.4) * 3.2;
    angles.arm_l = 6 + Math.sin(t * 3.4) * 9;
    angles.arm_r = -6 - Math.sin(t * 3.4) * 9;
  } else {
    // 日常自然轻摆小短手（微向内拢呼吸摆动）
    angles.arm_l = -2.0 + Math.sin(t * 1.9) * 2.6;
    angles.arm_r = 2.0 - Math.sin(t * 1.9 + 0.6) * 2.6;
  }

  // 小短腿划水与待机微动
  if (params.activity === 'swimming') {
    angles.leg_l = Math.sin(t * 6.2) * 14;
    angles.leg_r = -Math.sin(t * 6.2) * 14;
  } else if (effectiveEating || isHeadpatting) {
    angles.leg_l = Math.sin(t * 5.2) * 5.5;
    angles.leg_r = -Math.sin(t * 5.2) * 5.5;
  } else {
    angles.leg_l = Math.sin(t * 1.6) * 1.8;
    angles.leg_r = -Math.sin(t * 1.6) * 1.8;
  }

  angles.torso = torsoAngle;
  angles.head = headAngle;
  offsets.torso = { x: 0, y: torsoY, sx: torsoSx, sy: torsoSy };

  // 5. 二阶阻尼弹簧骨骼解算（呆毛、鱼鳍耳、后发、两侧卷发、大鲸鱼尾巴）
  const headVel = (headAngle - runtime.prevHeadAngle) / dt;
  const torsoVelY = (torsoY - runtime.prevTorsoY) / dt;
  runtime.prevHeadAngle = headAngle;
  runtime.prevTorsoY = torsoY;

  for (const bone of manifest.bones) {
    if (!bone.spring) continue;
    let sp = runtime.springs[bone.id];
    if (!sp) {
      sp = createSpringState(0);
      runtime.springs[bone.id] = sp;
    }

    let targetSpringDeg = 0;
    let inertialDrive = 0;

    switch (bone.id) {
      case 'ahoge':
        targetSpringDeg =
          Math.sin(t * 2.8) * 4.5 + (effectiveEating || isHeadpatting ? Math.sin(t * 7.5) * 6 : 0);
        inertialDrive = -headVel * 2.2 - torsoVelY * 0.55;
        break;
      case 'ear_l':
        targetSpringDeg =
          Math.sin(t * 2.3 + 0.4) * 3.2 + (effectiveEating ? Math.sin(t * 5.5) * 4.5 : 0);
        inertialDrive = -headVel * 1.4 + torsoVelY * 0.7;
        break;
      case 'ear_r':
        targetSpringDeg =
          -Math.sin(t * 2.3 + 0.4) * 3.2 - (effectiveEating ? Math.sin(t * 5.5) * 4.5 : 0);
        inertialDrive = -headVel * 1.4 - torsoVelY * 0.7;
        break;
      case 'hair_back':
        targetSpringDeg = Math.sin(t * 1.45 - 0.8) * 2.4;
        inertialDrive = -headVel * 1.1;
        break;
      case 'hair_side_l':
        targetSpringDeg = Math.sin(t * 1.85 - 0.5) * 3.4;
        inertialDrive = -headVel * 1.5 + torsoVelY * 0.35;
        break;
      case 'hair_side_r':
        targetSpringDeg = -Math.sin(t * 1.85 - 0.2) * 3.4;
        inertialDrive = -headVel * 1.5 - torsoVelY * 0.35;
        break;
      case 'tail_base':
        targetSpringDeg =
          effectiveEating || isHeadpatting
            ? Math.sin(t * 5.4) * 11.5
            : params.activity === 'swimming'
              ? Math.sin(t * 6.4) * 15
              : Math.sin(t * 1.95) * 6.2;
        inertialDrive = -torsoVelY * 0.6;
        break;
      case 'tail_tip':
        targetSpringDeg = Math.sin(t * 2.4 - 0.9) * 5.5;
        break;
      default:
        break;
    }

    stepSpring(sp, targetSpringDeg, bone.spring, dt, inertialDrive);
    angles[bone.id] = (angles[bone.id] ?? 0) + sp.angleDeg;
  }

  // 6. 递归求取全局 2D 仿射矩阵
  const boneMap = new Map<BoneId, BoneDefinition>(manifest.bones.map((b) => [b.id, b]));
  const boneMatrices = {} as Record<BoneId, Matrix2D>;

  const visitBone = (id: BoneId): Matrix2D => {
    const cached = boneMatrices[id];
    if (cached) return cached;
    const def = boneMap.get(id);
    if (!def) {
      boneMatrices[id] = IDENTITY_MATRIX;
      return IDENTITY_MATRIX;
    }
    const parentMat = def.parent ? visitBone(def.parent) : IDENTITY_MATRIX;
    const off = offsets[id];
    const localMat = makeBoneLocalMatrix(
      def.pivotX,
      def.pivotY,
      angles[id] ?? 0,
      off?.x ?? 0,
      off?.y ?? 0,
      off?.sx ?? 1,
      off?.sy ?? 1,
    );
    const worldMat = multiplyMatrix(parentMat, localMat);
    boneMatrices[id] = worldMat;
    return worldMat;
  };

  for (const b of manifest.bones) {
    visitBone(b.id);
  }

  return {
    boneMatrices,
    slotVariants,
    irisOffsetPx,
  };
}
