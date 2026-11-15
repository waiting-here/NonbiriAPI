import type { EncoreCard } from './layers';

type Point = readonly [number, number];
type Matrix = readonly [number, number, number, number, number, number];
type Arm = {
  shoulder: Point;
  elbow: Point;
  wrist: Point;
  polygon: readonly Point[];
  end: 'free' | 'fixed' | 'head';
};
type Rig = { waist: Point; neck: Point; period: number; arms: readonly Arm[] };
const tau = Math.PI * 2;
const clamp = (v: number, a = 0, b = 1) => Math.max(a, Math.min(b, v));
const smooth = (v: number) => {
  const t = clamp(v);
  return t * t * (3 - 2 * t);
};
const distance = (a: Point, b: Point) => Math.hypot(a[0] - b[0], a[1] - b[1]);
const apply = (m: Matrix, p: Point): Point => [
  m[0] * p[0] + m[2] * p[1] + m[4],
  m[1] * p[0] + m[3] * p[1] + m[5],
];
const mix = (a: Point, b: Point, t: number): Point => [
  a[0] + (b[0] - a[0]) * t,
  a[1] + (b[1] - a[1]) * t,
];
const wave = (t: number, period: number, phase = 0) =>
  Math.sin((t * tau) / period + phase) - Math.sin(phase);
export const encoreRigs: Record<EncoreCard, Rig> = {
  'u-gemini': {
    waist: [515, 590],
    neck: [520, 265],
    period: 4.7,
    arms: [
      {
        shoulder: [399, 340],
        elbow: [397, 538],
        wrist: [348, 741],
        end: 'free',
        polygon: [
          [374, 320],
          [428, 330],
          [439, 483],
          [425, 571],
          [393, 714],
          [379, 811],
          [329, 852],
          [298, 815],
          [313, 741],
          [339, 581],
          [350, 440],
        ],
      },
      {
        shoulder: [630, 335],
        elbow: [655, 550],
        wrist: [695, 477],
        end: 'fixed',
        polygon: [
          [598, 319],
          [648, 307],
          [680, 340],
          [742, 336],
          [747, 478],
          [701, 565],
          [661, 584],
          [627, 540],
          [610, 422],
        ],
      },
    ],
  },
  'u-glm': {
    waist: [501, 648],
    neck: [516, 327],
    period: 5.3,
    arms: [
      {
        shoulder: [394, 335],
        elbow: [354, 565],
        wrist: [307, 739],
        end: 'fixed',
        polygon: [
          [373, 314],
          [419, 341],
          [412, 455],
          [391, 581],
          [363, 690],
          [328, 772],
          [260, 817],
          [210, 804],
          [212, 755],
          [278, 725],
          [302, 590],
          [335, 424],
        ],
      },
      {
        shoulder: [638, 393],
        elbow: [622, 573],
        wrist: [651, 704],
        end: 'fixed',
        polygon: [
          [606, 366],
          [663, 380],
          [678, 491],
          [660, 587],
          [694, 707],
          [641, 739],
          [593, 650],
          [587, 540],
          [589, 446],
        ],
      },
    ],
  },
  'u-deepseek': {
    waist: [510, 604],
    neck: [520, 328],
    period: 4.9,
    arms: [
      {
        shoulder: [428, 357],
        elbow: [269, 320],
        wrist: [365, 229],
        end: 'head',
        polygon: [
          [416, 325],
          [455, 357],
          [428, 396],
          [348, 417],
          [264, 387],
          [215, 342],
          [213, 304],
          [262, 260],
          [339, 202],
          [398, 178],
          [420, 211],
          [387, 255],
          [336, 303],
        ],
      },
      {
        shoulder: [612, 356],
        elbow: [771, 313],
        wrist: [669, 223],
        end: 'head',
        polygon: [
          [596, 329],
          [636, 299],
          [689, 281],
          [662, 248],
          [610, 219],
          [589, 184],
          [616, 165],
          [660, 181],
          [748, 240],
          [801, 286],
          [828, 325],
          [788, 368],
          [709, 404],
          [639, 401],
        ],
      },
    ],
  },
};

function polygonContains(p: Point, polygon: readonly Point[]) {
  let inside = false;
  for (let i = 0, j = polygon.length - 1; i < polygon.length; j = i++) {
    const a = polygon[i],
      b = polygon[j];
    if (
      a[1] > p[1] !== b[1] > p[1] &&
      p[0] < ((b[0] - a[0]) * (p[1] - a[1])) / (b[1] - a[1]) + a[0]
    )
      inside = !inside;
  }
  return inside;
}
function projection(p: Point, a: Point, b: Point) {
  const x = b[0] - a[0],
    y = b[1] - a[1];
  return ((p[0] - a[0]) * x + (p[1] - a[1]) * y) / (x * x + y * y);
}
function segmentDistance(p: Point, a: Point, b: Point) {
  return distance(p, mix(a, b, clamp(projection(p, a, b))));
}

export type RigBinding = {
  source: Point;
  rise: number;
  head: number;
  arm: number;
  armWeight: number;
  lower: number;
  hand: number;
  fixed: boolean;
};
export function bindEncorePoint(id: EncoreCard, x: number, y: number, body = true): RigBinding {
  const rig = encoreRigs[id],
    p: Point = [x, y];
  const binding: RigBinding = {
    source: p,
    rise: smooth((rig.waist[1] - y) / (rig.waist[1] - rig.neck[1])),
    head: smooth((rig.neck[1] + 22 - y) / 44),
    arm: -1,
    armWeight: 0,
    lower: 0,
    hand: 0,
    fixed: false,
  };
  if (!body) return binding;
  // Painted supports are rigid. Grip and stool-contact regions include the entire hand.
  binding.fixed =
    (id === 'u-gemini' &&
      ((x >= 687 && x <= 724 && y >= 430) ||
        (x >= 662 && x <= 746 && y >= 242 && y <= 478) ||
        (x >= 645 && x <= 728 && y >= 235 && y <= 330))) ||
    (id === 'u-glm' && ((x < 336 && y >= 744) || (x >= 566 && y >= 636)));
  if (binding.fixed) return binding;
  for (let i = 0; i < rig.arms.length; i++) {
    const a = rig.arms[i];
    const inside = polygonContains(p, a.polygon);
    const edge = Math.min(
      ...a.polygon.map((v, index) =>
        segmentDistance(p, v, a.polygon[(index + 1) % a.polygon.length]),
      ),
    );
    const weight = smooth(((inside ? edge : -edge) + 20) / 24);
    if (weight === 0) continue;
    binding.arm = i;
    binding.armWeight = weight;
    const du = segmentDistance(p, a.shoulder, a.elbow),
      dl = segmentDistance(p, a.elbow, a.wrist);
    // Distance to both segments stays continuous through the entire elbow region.
    binding.lower = smooth(0.5 + (du - dl) / 32);
    binding.hand = a.end === 'free' ? 0 : smooth((projection(p, a.elbow, a.wrist) - 0.83) / 0.2);
    break;
  }
  return binding;
}

function segmentMatrix(a: Point, b: Point, c: Point, d: Point): Matrix {
  const x = b[0] - a[0],
    y = b[1] - a[1],
    u = d[0] - c[0],
    v = d[1] - c[1],
    den = x * x + y * y;
  const re = (u * x + v * y) / den,
    im = (v * x - u * y) / den;
  return [re, im, -im, re, c[0] - re * a[0] + im * a[1], c[1] - im * a[0] - re * a[1]];
}
function elbowFor(a: Arm, shoulder: Point, target: Point): Point {
  const l1 = distance(a.shoulder, a.elbow),
    l2 = distance(a.elbow, a.wrist);
  // A nearly straight supported arm allows the tiny stretch of breathing. Exact
  // fixed-length IK at full extension would snap the elbow sideways.
  if (a.end === 'fixed' && distance(a.shoulder, a.wrist) / (l1 + l2) > 0.96) {
    const follow = l2 / (l1 + l2);
    return [
      a.elbow[0] + (shoulder[0] - a.shoulder[0]) * follow + (target[0] - a.wrist[0]) * (1 - follow),
      a.elbow[1] + (shoulder[1] - a.shoulder[1]) * follow + (target[1] - a.wrist[1]) * (1 - follow),
    ];
  }
  const dx = target[0] - shoulder[0],
    dy = target[1] - shoulder[1],
    raw = Math.hypot(dx, dy);
  const d = clamp(raw, Math.abs(l1 - l2) + 0.01, l1 + l2 - 0.01),
    along = (l1 * l1 - l2 * l2 + d * d) / (2 * d),
    height = Math.sqrt(Math.max(0, l1 * l1 - along * along));
  const ox = a.wrist[0] - a.shoulder[0],
    oy = a.wrist[1] - a.shoulder[1];
  const side =
    Math.sign(ox * (a.elbow[1] - a.shoulder[1]) - oy * (a.elbow[0] - a.shoulder[0])) || 1;
  return [
    shoulder[0] + (dx / raw) * along - (dy / raw) * height * side,
    shoulder[1] + (dy / raw) * along + (dx / raw) * height * side,
  ];
}
export function encorePoseAt(id: EncoreCard, time: number) {
  const rig = encoreRigs[id];
  const phase = (((time / rig.period) % 1) + 1) % 1;
  const breath = phase < 0.36 ? smooth(phase / 0.36) : 1 - smooth((phase - 0.36) / 0.64);
  const lift = 3.1 * breath * (1 + 0.1 * Math.sin((time * tau) / 47.3));
  const expand = 0.0065 * breath;
  const drift = 0.7 * wave(time, 17.1);
  const headAngle = 0.0032 * wave(time, 11.9);
  const headX = drift + 0.8 * wave(time, 13.7),
    headY = -lift;
  const c = Math.cos(headAngle),
    s = Math.sin(headAngle),
    [nx, ny] = rig.neck;
  const head: Matrix = [c, s, -s, c, nx + headX - c * nx + s * ny, ny + headY - s * nx - c * ny];
  const torso = (p: Point): Point => {
    const w = smooth((rig.waist[1] - p[1]) / (rig.waist[1] - ny));
    return [p[0] + ((p[0] - rig.waist[0]) * expand + drift) * w, p[1] - lift * w];
  };
  const arms = rig.arms.map((a, i) => {
    const shoulder = torso(a.shoulder);
    let elbow: Point, wrist: Point;
    if (a.end === 'free') {
      const upper = 0.006 * wave(time, 8.7),
        lower = 0.009 * wave(time, 10.9, -0.7);
      const rotate = (p: Point, origin: Point, angle: number): Point => [
        origin[0] + (p[0] - origin[0]) * Math.cos(angle) - (p[1] - origin[1]) * Math.sin(angle),
        origin[1] + (p[0] - origin[0]) * Math.sin(angle) + (p[1] - origin[1]) * Math.cos(angle),
      ];
      elbow = rotate(
        [shoulder[0] + a.elbow[0] - a.shoulder[0], shoulder[1] + a.elbow[1] - a.shoulder[1]],
        shoulder,
        upper,
      );
      wrist = rotate(
        [elbow[0] + a.wrist[0] - a.elbow[0], elbow[1] + a.wrist[1] - a.elbow[1]],
        elbow,
        upper + lower,
      );
    } else {
      wrist = a.end === 'head' ? apply(head, a.wrist) : a.wrist;
      elbow = elbowFor(a, shoulder, wrist);
    }
    return {
      upper: segmentMatrix(a.shoulder, a.elbow, shoulder, elbow),
      lower: segmentMatrix(a.elbow, a.wrist, elbow, wrist),
      hand: a.end === 'head' ? head : ([1, 0, 0, 1, 0, 0] as Matrix),
      index: i,
    };
  });
  return { id, rig, lift, expand, drift, head, arms };
}
export function posedEncorePoint(
  binding: RigBinding,
  pose: ReturnType<typeof encorePoseAt>,
): Point {
  const p = binding.source;
  if (binding.fixed) return p;
  const torso: Point = [
    p[0] + ((p[0] - pose.rig.waist[0]) * pose.expand + pose.drift) * binding.rise,
    p[1] - pose.lift * binding.rise,
  ];
  const base = mix(torso, apply(pose.head, p), binding.head);
  if (binding.arm >= 0) {
    const arm = pose.arms[binding.arm];
    const posed = mix(
      mix(apply(arm.upper, p), apply(arm.lower, p), binding.lower),
      apply(arm.hand, p),
      binding.hand,
    );
    return mix(base, posed, binding.armWeight);
  }
  return base;
}
