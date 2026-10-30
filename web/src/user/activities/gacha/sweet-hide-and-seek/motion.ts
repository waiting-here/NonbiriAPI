import type { MomentFrame } from '../moment-frame';
export type SweetCard = 's-chatgpt' | 's-claude';
export type Point = readonly [number, number];
export type MotionRegion = {
  name: string;
  /** Source coordinates, including room outside each free silhouette. */
  polygon: readonly Point[];
  root: Point;
  tip: Point;
  amplitude: Point;
  phase: number;
  feather: number;
};
type RibSection = readonly [
  y: number,
  left: number,
  right: number,
  lift: number,
  expansion: number,
  follow: number,
];
type BreathingRig = {
  /** Neck, shoulder, ribs and waist sections share one respiratory driver. */
  sections: readonly RibSection[];
  feather: number;
};

export const motionRegions: Record<SweetCard, readonly MotionRegion[]> = {
  's-chatgpt': [
    {
      name: 'left-hair',
      polygon: [
        [155, 345],
        [294, 345],
        [288, 503],
        [286, 624],
        [263, 727],
        [227, 856],
        [170, 956],
        [135, 1010],
        [172, 1080],
        [191, 1130],
        [150, 1180],
        [-40, 1180],
        [-40, 740],
        [108, 612],
      ],
      root: [257, 355],
      tip: [92, 1080],
      amplitude: [17, 3.4],
      phase: 0.2,
      feather: 40,
    },
    {
      name: 'right-hair',
      polygon: [
        [630, 332],
        [731, 342],
        [824, 442],
        [882, 639],
        [938, 790],
        [1060, 889],
        [1060, 1127],
        [930, 1142],
        [855, 1039],
        [818, 921],
        [809, 790],
        [810, 645],
        [749, 571],
        [679, 443],
      ],
      root: [666, 350],
      tip: [955, 1040],
      amplitude: [18, 3.6],
      phase: -0.15,
      feather: 40,
    },
    {
      name: 'left-sleeve',
      polygon: [
        [248, 630],
        [310, 636],
        [339, 699],
        [317, 794],
        [283, 889],
        [248, 957],
        [182, 1001],
        [138, 968],
        [183, 812],
      ],
      root: [285, 648],
      tip: [212, 958],
      amplitude: [8.5, 2.8],
      phase: -0.45,
      feather: 28,
    },
    {
      name: 'right-sleeve',
      polygon: [
        [811, 665],
        [854, 691],
        [871, 816],
        [915, 927],
        [1000, 1011],
        [1060, 1022],
        [1060, 1158],
        [930, 1160],
        [835, 1122],
        [799, 1009],
        [765, 925],
        [780, 868],
      ],
      root: [815, 684],
      tip: [918, 1095],
      amplitude: [10, 3.2],
      phase: -0.6,
      feather: 36,
    },
  ],
  's-claude': [
    {
      name: 'back-hair',
      polygon: [
        [350, 180],
        [470, 182],
        [522, 272],
        [476, 352],
        [454, 398],
        [470, 461],
        [490, 503],
        [454, 554],
        [391, 597],
        [345, 642],
        [312, 727],
        [227, 741],
        [159, 656],
        [140, 517],
        [165, 384],
        [248, 284],
      ],
      root: [427, 209],
      tip: [277, 701],
      amplitude: [17, 3.3],
      phase: 0.35,
      feather: 40,
    },
    {
      name: 'outer-curls',
      polygon: [
        [235, 312],
        [312, 291],
        [356, 341],
        [329, 442],
        [286, 524],
        [222, 580],
        [154, 551],
        [138, 460],
        [175, 378],
      ],
      root: [309, 304],
      tip: [202, 541],
      amplitude: [5, 1.6],
      phase: -0.7,
      feather: 30,
    },
    {
      name: 'left-hem',
      polygon: [
        [264, 663],
        [325, 650],
        [348, 733],
        [366, 835],
        [369, 984],
        [337, 1063],
        [330, 1261],
        [299, 1305],
        [162, 1306],
        [146, 1178],
        [181, 997],
        [218, 820],
      ],
      root: [314, 672],
      tip: [233, 1252],
      amplitude: [12.5, 3.8],
      phase: -0.55,
      feather: 40,
    },
    {
      name: 'right-hem',
      polygon: [
        [560, 814],
        [647, 814],
        [697, 847],
        [694, 998],
        [659, 1136],
        [638, 1287],
        [542, 1302],
        [446, 1275],
        [435, 1202],
        [459, 1069],
        [523, 923],
      ],
      root: [597, 825],
      tip: [562, 1248],
      amplitude: [11, 3.6],
      phase: -0.85,
      feather: 40,
    },
  ],
};

export const breathingRigs: Record<SweetCard, BreathingRig> = {
  's-chatgpt': {
    sections: [
      [448, 420, 662, 0, 0, 0],
      [492, 270, 740, 2, 0.004, 0],
      [590, 300, 758, 7.2, 0.012, 0],
      [680, 335, 749, 12, 0.018, 0.25],
      [800, 334, 743, 13, 0.018, 0.65],
      [866, 328, 701, 6.4, 0.01, 0.2],
      [936, 340, 680, 0, 0, 0],
    ],
    feather: 32,
  },
  's-claude': {
    sections: [
      [240, 477, 624, 0, 0, 0],
      [280, 470, 634, 2, 0.003, 0],
      [350, 435, 745, 8.4, 0.011, 0.1],
      [420, 445, 745, 11, 0.015, 0.4],
      [480, 450, 744, 9.4, 0.012, 0.65],
      [496, 440, 740, 7, 0.01, 0.45],
      [520, 410, 715, 2.5, 0.005, 0.1],
      [536, 397, 588, 0, 0, 0],
    ],
    feather: 32,
  },
};

// Guard bands also pin all vertices of triangles crossing a contact boundary.
// Hair resting on the candy and sheer cloth over skin have no hidden underpaint.
export const fixedRegions: Record<SweetCard, readonly (readonly Point[])[]> = {
  's-chatgpt': [
    [
      [-100, -100],
      [1124, -100],
      [1124, 345],
      [-100, 345],
    ],
    [
      [300, 210],
      [670, 210],
      [684, 420],
      [630, 493],
      [432, 512],
      [319, 420],
    ],
    [
      [745, 403],
      [866, 408],
      [873, 684],
      [819, 906],
      [704, 903],
      [707, 697],
      [726, 633],
    ],
    [
      [148, 952],
      [254, 952],
      [254, 1111],
      [148, 1111],
    ],
    [
      [265, 970],
      [701, 970],
      [783, 1106],
      [760, 1536],
      [170, 1536],
      [170, 1132],
    ],
    [
      [-100, 1115],
      [1124, 1115],
      [1124, 1636],
      [-100, 1636],
    ],
  ],
  's-claude': [
    [
      [-100, -100],
      [1124, -100],
      [1124, 237],
      [-100, 237],
    ],
    [
      [480, 90],
      [654, 90],
      [654, 257],
      [480, 257],
    ],
    [
      [310, 125],
      [470, 125],
      [453, 299],
      [301, 312],
    ],
    [
      [629, 180],
      [720, 177],
      [733, 328],
      [666, 365],
      [626, 326],
    ],
    [
      [540, 650],
      [647, 650],
      [647, 865],
      [540, 865],
    ],
    [
      [391, 565],
      [532, 551],
      [550, 647],
      [556, 715],
      [392, 739],
      [350, 687],
      [332, 640],
    ],
    [
      [386, 701],
      [539, 702],
      [574, 819],
      [556, 929],
      [519, 1003],
      [479, 1099],
      [459, 1221],
      [434, 1340],
      [464, 1471],
      [500, 1536],
      [256, 1536],
      [254, 1350],
      [285, 1182],
      [304, 1023],
      [331, 907],
      [338, 799],
    ],
    [
      [-100, 1320],
      [1124, 1320],
      [1124, 1636],
      [-100, 1636],
    ],
  ],
};

const smooth = (a: number, b: number, x: number) => {
  const t = Math.max(0, Math.min(1, (x - a) / (b - a)));
  return t * t * (3 - 2 * t);
};

/** Positive inside, negative outside. Evaluated only while baking the mesh. */
export function polygonDistance(polygon: readonly Point[], [x, y]: Point): number {
  let inside = false,
    distance = Infinity;
  for (let i = 0; i < polygon.length; i++) {
    const a = polygon[i],
      b = polygon[(i + 1) % polygon.length];
    if (a[1] > y !== b[1] > y && x < ((b[0] - a[0]) * (y - a[1])) / (b[1] - a[1]) + a[0])
      inside = !inside;
    const dx = b[0] - a[0],
      dy = b[1] - a[1];
    const t = Math.max(0, Math.min(1, ((x - a[0]) * dx + (y - a[1]) * dy) / (dx * dx + dy * dy)));
    distance = Math.min(distance, Math.hypot(x - a[0] - dx * t, y - a[1] - dy * t));
  }
  return inside ? distance : -distance;
}

function along(region: MotionRegion, point: Point): number {
  const dx = region.tip[0] - region.root[0],
    dy = region.tip[1] - region.root[1];
  return Math.max(
    0,
    Math.min(
      1,
      ((point[0] - region.root[0]) * dx + (point[1] - region.root[1]) * dy) / (dx * dx + dy * dy),
    ),
  );
}

export function regionWeight(region: MotionRegion, point: Point): number {
  return (
    smooth(0, region.feather, polygonDistance(region.polygon, point)) *
    smooth(0, 1, along(region, point))
  );
}

function breathingOffset(id: SweetCard, [x, y]: Point): readonly [number, number, number] {
  const { sections, feather } = breathingRigs[id];
  for (let i = 1; i < sections.length; i++) {
    const a = sections[i - 1],
      b = sections[i];
    if (y < a[0] || y > b[0]) continue;
    const t = smooth(a[0], b[0], y);
    const left = a[1] + (b[1] - a[1]) * t,
      right = a[2] + (b[2] - a[2]) * t;
    const weight = smooth(0, feather, Math.min(x - left, right - x));
    const lift = a[3] + (b[3] - a[3]) * t;
    const expansion = a[4] + (b[4] - a[4]) * t;
    return [
      (x - (left + right) * 0.5) * expansion * weight,
      -lift * weight,
      a[5] + (b[5] - a[5]) * t,
    ];
  }
  return [0, 0, 0];
}

export const meshStep = 8;
export const vertexStride = 14;
export const meshColumns = 1024 / meshStep + 1;
export type MotionMesh = { vertices: Float32Array; indices: Uint16Array };

function axis(limit: number, before = 0, after = 0) {
  const values = [0];
  for (let p = meshStep; p <= limit; p += meshStep) values.push(p);
  for (let p = meshStep; p < before; p += meshStep) values.push(-p);
  if (before > 0) values.push(-before);
  for (let p = meshStep; p < after; p += meshStep) values.push(limit + p);
  if (after > 0) values.push(limit + after);
  return values.sort((a, b) => a - b);
}

export function createMotionMesh(id: SweetCard, frame?: MomentFrame): MotionMesh {
  const region = frame?.originalRegion;
  const xs = axis(1024, region?.x, frame ? frame.canvas[0] - frame.originalRegion.x - 1024 : 0);
  const ys = axis(1536, region?.y, frame ? frame.canvas[1] - frame.originalRegion.y - 1536 : 0);
  const columns = xs.length,
    rows = ys.length;
  if (columns * rows > 65535) throw new Error('motion-mesh-unavailable');
  const vertices = new Float32Array(columns * rows * vertexStride);
  const indices = new Uint16Array((columns - 1) * (rows - 1) * 6);
  for (let row = 0; row < rows; row++) {
    for (let column = 0; column < columns; column++) {
      const position: Point = [xs[column]!, ys[row]!];
      const point: Point = [
        Math.max(0, Math.min(1024, position[0])),
        Math.max(0, Math.min(1536, position[1])),
      ];
      const index = (row * columns + column) * vertexStride;
      vertices[index] = position[0];
      vertices[index + 1] = position[1];
      let protection = 1,
        bodyProtection = 1;
      for (const polygon of fixedRegions[id]) {
        const distance = -polygonDistance(polygon, point);
        protection = Math.min(protection, smooth(12, 76, distance));
        bodyProtection = Math.min(bodyProtection, smooth(12, 60, distance));
      }
      if (protection === 0) continue;
      let totalWeight = 0;
      for (const region of motionRegions[id]) {
        const weight = regionWeight(region, point) * protection;
        if (weight === 0) continue;
        totalWeight += weight;
        const lag = along(region, point) * 0.95;
        const phase = region.phase - lag;
        const secondary = region.phase * 0.6 - lag * 1.35;
        // Shared slow air movement, with smaller trailing flex along each strand.
        vertices[index + 2] += region.amplitude[0] * weight * 0.82 * Math.cos(phase);
        vertices[index + 3] += region.amplitude[1] * weight * 0.82 * Math.cos(phase + 0.4);
        vertices[index + 4] += region.amplitude[0] * weight * 0.82 * Math.sin(phase);
        vertices[index + 5] += region.amplitude[1] * weight * 0.82 * Math.sin(phase + 0.4);
        vertices[index + 6] += region.amplitude[0] * weight * 0.18 * Math.cos(secondary);
        vertices[index + 7] += region.amplitude[1] * weight * 0.18 * Math.cos(secondary + 0.4);
        vertices[index + 8] += region.amplitude[0] * weight * 0.18 * Math.sin(secondary);
        vertices[index + 9] += region.amplitude[1] * weight * 0.18 * Math.sin(secondary + 0.4);
      }
      // Shared pixels at a hair/cloth overlap follow the blended material motion.
      if (totalWeight > 1)
        for (let channel = 2; channel < 10; channel++) vertices[index + channel] /= totalWeight;
      const ribs = breathingOffset(id, point);
      vertices[index + 10] = ribs[0] * bodyProtection;
      vertices[index + 11] = ribs[1] * bodyProtection;
      vertices[index + 12] = vertices[index + 10] * ribs[2];
      vertices[index + 13] = vertices[index + 11] * ribs[2];
    }
  }
  let index = 0;
  for (let row = 0; row < rows - 1; row++)
    for (let column = 0; column < columns - 1; column++) {
      const a = row * columns + column,
        b = a + 1,
        c = a + columns,
        d = c + 1;
      indices[index++] = a;
      indices[index++] = b;
      indices[index++] = c;
      indices[index++] = c;
      indices[index++] = b;
      indices[index++] = d;
    }
  return { vertices, indices };
}

const breathingCycle = (seconds: number) => {
  const cycle = (((seconds % 6.8) + 6.8) % 6.8) / 6.8;
  const breath = cycle < 0.38 ? cycle / 0.38 : (1 - cycle) / 0.62;
  return 0.5 - Math.cos(Math.PI * breath) * 0.5;
};

/** Caller-owned storage is reused on every frame. */
export function motionDrivers(seconds: number, result: Float32Array): void {
  const angle = (seconds * Math.PI * 2) / 9.6;
  result[0] = Math.sin(angle);
  result[1] = Math.cos(angle);
  result[2] = Math.sin(angle * 2);
  result[3] = Math.cos(angle * 2);
  const breath = breathingCycle(seconds);
  result[4] = breath;
  // Lower contours follow the same breath, settling toward their original rest shape.
  result[5] = breathingCycle(seconds - 0.28) - breath;
}
