import { createSpringState, stepSpring } from '../rig/spring';

export const IDLE_MOTION_PADDING = 24;

export type OriginalOutfit = 'whale-maid' | 'whale-winter' | 'whale-knit' | 'whale-hoodie';

interface MotionProfile {
  axis: 'x' | 'y';
  travel: number;
  follow: readonly [x: number, y: number];
  idleAmplitude: number;
  period: number;
  phase: number;
  stiffness: number;
  damping: number;
}

interface MotionPatch {
  part: keyof typeof PROFILES;
  bounds: readonly [left: number, top: number, right: number, bottom: number];
  pivot: number;
  peak: number;
}

const PROFILES = {
  ahoge: {
    axis: 'y',
    travel: 10,
    follow: [-0.3, -0.5],
    idleAmplitude: 0.47,
    period: 1.9,
    phase: 0,
    stiffness: 100,
    damping: 11,
  },
  leftHair: {
    axis: 'x',
    travel: 23,
    follow: [-0.65, 0.14],
    idleAmplitude: 0.48,
    period: 3.3,
    phase: 0.3,
    stiffness: 67,
    damping: 9,
  },
  rightHair: {
    axis: 'x',
    travel: 23,
    follow: [-0.65, -0.14],
    idleAmplitude: 0.48,
    period: 3.3,
    phase: 1.4,
    stiffness: 62,
    damping: 9,
  },
  leftCloth: {
    axis: 'y',
    travel: 15,
    follow: [0.42, 0.25],
    idleAmplitude: 0.48,
    period: 2.85,
    phase: 1.1,
    stiffness: 55,
    damping: 9,
  },
  rightCloth: {
    axis: 'y',
    travel: 15,
    follow: [-0.42, 0.25],
    idleAmplitude: 0.48,
    period: 2.85,
    phase: 2.45,
    stiffness: 48,
    damping: 9,
  },
  tail: {
    axis: 'y',
    travel: 35,
    follow: [0.46, 0.45],
    idleAmplitude: 0.77,
    period: 2.7,
    phase: 0.35,
    stiffness: 48,
    damping: 8,
  },
} satisfies Record<string, MotionProfile>;

// Regions are measured in source pixels. Cloth patches stop before the front fastenings and shoes.
const PATCHES: Readonly<Record<OriginalOutfit, readonly MotionPatch[]>> = {
  'whale-maid': [
    { part: 'ahoge', bounds: [120, -14, 274, 94], pivot: 34, peak: 155 },
    { part: 'leftHair', bounds: [-20, 393, 158, 676], pivot: 61, peak: 569 },
    { part: 'rightHair', bounds: [470, 394, 588, 675], pivot: 530, peak: 560 },
    { part: 'leftCloth', bounds: [158, 538, 215, 679], pivot: 621, peak: 179 },
    { part: 'rightCloth', bounds: [382, 538, 470, 679], pivot: 626, peak: 440 },
    { part: 'tail', bounds: [588, 399, 702, 672], pivot: 511, peak: 670 },
  ],
  'whale-winter': [
    { part: 'ahoge', bounds: [91, -14, 207, 71], pivot: 27, peak: 116 },
    { part: 'leftHair', bounds: [-16, 281, 110, 477], pivot: 45, peak: 396 },
    { part: 'rightHair', bounds: [325, 272, 412, 477], pivot: 373, peak: 396 },
    { part: 'leftCloth', bounds: [110, 371, 188, 468], pivot: 431, peak: 128 },
    { part: 'rightCloth', bounds: [253, 371, 325, 468], pivot: 432, peak: 309 },
    { part: 'tail', bounds: [412, 281, 497, 482], pivot: 367, peak: 463 },
  ],
  'whale-knit': [
    { part: 'ahoge', bounds: [85, -12, 208, 79], pivot: 30, peak: 111 },
    { part: 'leftHair', bounds: [-15, 278, 108, 452], pivot: 48, peak: 377 },
    { part: 'rightHair', bounds: [327, 272, 406, 462], pivot: 366, peak: 381 },
    { part: 'leftCloth', bounds: [108, 388, 176, 454], pivot: 425, peak: 126 },
    { part: 'rightCloth', bounds: [263, 388, 327, 453], pivot: 425, peak: 303 },
    { part: 'tail', bounds: [406, 278, 485, 471], pivot: 352, peak: 451 },
  ],
  'whale-hoodie': [
    { part: 'ahoge', bounds: [157, -12, 267, 71], pivot: 32, peak: 212 },
    { part: 'leftHair', bounds: [-15, 292, 101, 448], pivot: 47, peak: 381 },
    { part: 'rightHair', bounds: [330, 290, 380, 455], pivot: 352, peak: 385 },
    { part: 'leftCloth', bounds: [101, 365, 176, 460], pivot: 422, peak: 121 },
    { part: 'rightCloth', bounds: [278, 365, 330, 460], pivot: 425, peak: 311 },
    { part: 'tail', bounds: [380, 282, 468, 465], pivot: 356, peak: 426 },
  ],
};

function smoothstep(value: number) {
  return value * value * (3 - 2 * value);
}

function preparePatch(patch: MotionPatch) {
  const profile = PROFILES[patch.part];
  const [left, top, right, bottom] = patch.bounds;
  const horizontal = profile.axis === 'x';
  const start = horizontal ? top : left;
  const end = horizontal ? bottom : right;
  const isTail = patch.part === 'tail';
  const step = isTail ? 2 : 3;
  const strips = [];
  for (let at = start; at < end; at += step) {
    const size = Math.min(step, end - at);
    const center = at + size / 2;
    const fromRoot = (center - start) / (patch.peak - start);
    const fromEdge = isTail ? 1 : (end - center) / (end - patch.peak);
    const amount = Math.min(1, fromRoot, fromEdge);
    strips.push({ at, size, weight: smoothstep(amount) });
  }
  const spring = { stiffness: profile.stiffness, damping: profile.damping, maxAngleDeg: 1.25 };
  return { ...patch, profile, horizontal, strips, spring, state: createSpringState() };
}

export function createIdleMotion(image: HTMLImageElement, outfit: OriginalOutfit) {
  const canvas = document.createElement('canvas');
  const pad = IDLE_MOTION_PADDING;
  canvas.width = image.naturalWidth + 2 * pad;
  canvas.height = image.naturalHeight + 2 * pad;
  const ctx = canvas.getContext('2d');
  if (!ctx) return null;
  ctx.translate(pad, pad);

  const scale = image.naturalHeight / 727;
  const patches = PATCHES[outfit].map(preparePatch);
  const source = document.createElement('canvas');
  source.width = canvas.width;
  source.height = canvas.height;
  const sourceCtx = source.getContext('2d');
  if (!sourceCtx) return null;
  sourceCtx.drawImage(image, pad, pad);
  let lastTime: number | undefined;

  function drawPatch(patch: ReturnType<typeof preparePatch>, shift: number) {
    if (!ctx || Math.abs(shift) < 0.05) return;
    const [left, top, right, bottom] = patch.bounds;
    const pivot = patch.pivot;
    const tailBand =
      patch.part === 'tail' ? ([pivot - 56 * scale, pivot + 64 * scale] as const) : null;
    ctx.clearRect(left, top, right - left, bottom - top);
    for (const { at, size, weight } of patch.strips) {
      const offset = shift * weight;
      if (tailBand) {
        // Carry the fin through the bend without compressing its silhouette.
        const start = Math.round(tailBand[0] + offset);
        const end = Math.round(tailBand[1] + offset);
        const sampleStart = start - offset;
        const sampleEnd = end - offset;
        ctx.drawImage(
          source,
          at + pad,
          top + pad,
          size,
          sampleStart - top,
          at,
          top,
          size,
          start - top,
        );
        ctx.drawImage(
          source,
          at + pad,
          sampleStart + pad,
          size,
          end - start,
          at,
          start,
          size,
          end - start,
        );
        ctx.drawImage(
          source,
          at + pad,
          sampleEnd + pad,
          size,
          bottom - sampleEnd,
          at,
          end,
          size,
          bottom - end,
        );
        continue;
      }

      const moved = pivot + offset;
      // Keep destination edges on the pixel grid. Fractional sampling still lets the pivot glide.
      const split = Math.round(moved);
      const sample = pivot + split - moved;
      if (patch.horizontal) {
        ctx.drawImage(
          source,
          left + pad,
          at + pad,
          sample - left,
          size,
          left,
          at,
          split - left,
          size,
        );
        ctx.drawImage(
          source,
          sample + pad,
          at + pad,
          right - sample,
          size,
          split,
          at,
          right - split,
          size,
        );
      } else {
        ctx.drawImage(source, at + pad, top + pad, size, sample - top, at, top, size, split - top);
        ctx.drawImage(
          source,
          at + pad,
          sample + pad,
          size,
          bottom - sample,
          at,
          split,
          size,
          bottom - split,
        );
      }
    }
  }

  return {
    canvas,
    update(now: number, x: number, y: number): boolean {
      const elapsed = lastTime === undefined ? 1000 / 60 : now - lastTime;
      if (elapsed < 16) return false;
      lastTime = now;
      ctx.clearRect(-pad, -pad, canvas.width, canvas.height);
      ctx.drawImage(source, -pad, -pad);
      for (const patch of patches) {
        const { profile, state } = patch;
        const wave = Math.sin((now / (1000 * profile.period)) * Math.PI * 2 + profile.phase);
        const target = x * profile.follow[0] + y * profile.follow[1] + profile.idleAmplitude * wave;
        stepSpring(state, target, patch.spring, elapsed / 1000);
        drawPatch(patch, state.angleDeg * profile.travel * scale);
      }
      return true;
    },
  };
}
