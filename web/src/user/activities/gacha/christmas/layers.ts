import { christmasRigs } from './rigs';
import type { ChristmasCard, Point } from './motion';
import type { MotionRegion } from '../sweet-hide-and-seek/motion';

export type Rect = readonly [number, number, number, number];
export type ArtLayer = {
  name: string;
  label: string;
  source: 'hero' | 'body' | 'dress' | 'front' | 'prop';
  rect: Rect;
  region?: MotionRegion;
  fixed: readonly (readonly Point[])[];
  mask?: readonly Point[];
  rigid?: { pivot: Point; radians: number; period: number; parent?: string };
  floating?: Point;
  rest?: boolean;
};
const full: Rect = [0, 0, 1024, 1536];
const bounds = (points: readonly Point[]): Rect => {
  const x = Math.max(0, Math.floor(Math.min(...points.map((p) => p[0])) - 12));
  const y = Math.max(0, Math.floor(Math.min(...points.map((p) => p[1])) - 12));
  return [
    x,
    y,
    Math.min(1024, Math.ceil(Math.max(...points.map((p) => p[0])) + 12)) - x,
    Math.min(1536, Math.ceil(Math.max(...points.map((p) => p[1])) + 12)) - y,
  ];
};
const region = (
  name: string,
  polygon: Point[],
  root: Point,
  tip: Point,
  amplitude: Point,
  phase = 0,
): MotionRegion => ({ name, polygon, root, tip, amplitude, phase, feather: 34 });
const labels: Record<string, string> = {
  'left-hair': '左侧发束',
  'right-hair': '右侧发束',
  'left-cape': '左侧披纱',
  'right-cape': '右侧披纱',
  'free-skirt': '外层裙摆',
  'left-stole': '左侧披肩',
  'right-stole': '右侧披肩',
  'left-dress-hem': '侧边裙片',
  'left-dress-panels': '左侧层叠裙片',
  'right-dress-panels': '右侧层叠裙片',
  'tail-flukes': '鲸尾与尾鳍',
  'left-rainbow-hair': '左侧虹彩长发',
  'right-rainbow-hair': '右侧虹彩长发',
  'left-dress-train': '长裙拖尾',
  'free-blue-ribbon': '独立蓝色飘带',
};

const claudeHair = [
  region(
    'left-curls',
    [
      [190, 318],
      [346, 298],
      [398, 355],
      [362, 455],
      [340, 555],
      [303, 680],
      [274, 777],
      [206, 769],
      [148, 648],
      [143, 483],
    ],
    [340, 330],
    [230, 735],
    [16, 3.4],
    0.2,
  ),
  region(
    'right-curls',
    [
      [654, 308],
      [723, 348],
      [782, 446],
      [793, 557],
      [773, 681],
      [718, 759],
      [672, 746],
      [685, 661],
      [700, 555],
      [675, 436],
    ],
    [670, 350],
    [738, 707],
    [15, 3],
    -0.2,
  ),
];
const claudeDress = [
  region(
    'left-sleeve',
    [
      [330, 480],
      [385, 480],
      [437, 548],
      [510, 585],
      [513, 644],
      [467, 673],
      [379, 709],
      [305, 686],
      [291, 600],
    ],
    [365, 492],
    [325, 665],
    [5.5, 2],
    -0.3,
  ),
  region(
    'right-sleeve',
    [
      [661, 524],
      [715, 540],
      [746, 610],
      [727, 704],
      [670, 730],
      [634, 689],
      [644, 637],
    ],
    [673, 540],
    [709, 682],
    [5, 1.8],
    -0.55,
  ),
  region(
    'left-train',
    [
      [341, 744],
      [406, 789],
      [421, 923],
      [397, 1115],
      [435, 1323],
      [382, 1405],
      [294, 1385],
      [290, 1175],
      [106, 1114],
      [102, 969],
      [240, 858],
    ],
    [361, 778],
    [294, 1267],
    [15.5, 3.5],
    -0.5,
  ),
  region(
    'right-train',
    [
      [712, 791],
      [808, 818],
      [852, 924],
      [899, 1070],
      [922, 1285],
      [849, 1403],
      [762, 1360],
      [731, 1180],
      [750, 1016],
    ],
    [749, 825],
    [855, 1288],
    [15, 3.5],
    -0.85,
  ),
  region(
    'front-skirt',
    [
      [398, 804],
      [690, 799],
      [763, 896],
      [751, 1071],
      [784, 1270],
      [706, 1395],
      [431, 1411],
      [389, 1292],
      [442, 1101],
      [448, 949],
    ],
    [567, 838],
    [565, 1321],
    [10, 3.2],
    -1.05,
  ),
];

const materialLayer = (
  source: ArtLayer['source'],
  entry: MotionRegion,
  fixed: ArtLayer['fixed'],
  label?: string,
): ArtLayer => ({
  name: entry.name,
  label: label ?? labels[entry.name] ?? entry.name,
  source,
  rect: bounds(entry.polygon),
  region: entry,
  mask: entry.polygon,
  fixed,
});
const pendant = (
  name: string,
  label: string,
  polygon: Point[],
  pivot: Point,
  parent: string,
): ArtLayer => ({
  name,
  label,
  source: 'hero',
  rect: bounds(polygon),
  mask: polygon,
  fixed: [],
  rigid: { pivot, parent, radians: 0.045, period: 7.8 },
});

/** Front-to-back cut order within each source; rendering reverses it over the residual plate. */
export function cutLayers(id: ChristmasCard): ArtLayer[] {
  const rig = christmasRigs[id];
  if (id === 't-claude')
    return [
      ...claudeHair.map((entry, i) =>
        materialLayer('body', entry, rig.fixed, i ? '右侧卷发' : '左侧卷发'),
      ),
      ...claudeDress.map((entry, i) =>
        materialLayer(
          'dress',
          entry,
          [],
          ['左侧薄纱袖', '右侧薄纱袖', '左侧裙纱', '右侧裙纱', '正面长裙'][i],
        ),
      ),
    ];
  const extra: ArtLayer[] = [];
  if (id === 't-chatgpt')
    extra.push(
      pendant(
        'left-emerald',
        '左发梢翡翠坠饰',
        [
          [127, 956],
          [165, 956],
          [168, 1022],
          [121, 1022],
        ],
        [146, 967],
        'left-hair',
      ),
      pendant(
        'right-emerald',
        '右发梢翡翠坠饰',
        [
          [780, 1066],
          [820, 1066],
          [826, 1138],
          [775, 1138],
        ],
        [800, 1077],
        'right-hair',
      ),
    );
  if (id === 't-glm')
    extra.push(
      pendant(
        'left-bell',
        '左披肩铃铛',
        [
          [282, 927],
          [326, 927],
          [333, 981],
          [277, 981],
        ],
        [306, 931],
        'left-stole',
      ),
      pendant(
        'right-bell',
        '右披肩铃铛',
        [
          [734, 944],
          [782, 943],
          [790, 1002],
          [730, 1003],
        ],
        [759, 950],
        'right-stole',
      ),
    );
  if (rig.floating)
    extra.push({
      name: 'cloud',
      label: '云朵陪伴物',
      source: 'hero',
      rect: bounds(rig.floating.polygon),
      mask: rig.floating.polygon,
      fixed: [],
      floating: rig.floating.amplitude,
    });
  return [
    ...extra,
    ...[...rig.materials].reverse().map((entry) => materialLayer('hero', entry, rig.fixed)),
  ];
}

export function renderLayers(id: ChristmasCard): ArtLayer[] {
  const cuts = cutLayers(id);
  const base = (name: string, label: string, source: ArtLayer['source']): ArtLayer => ({
    name,
    label,
    source,
    rect: full,
    fixed: christmasRigs[id].fixed,
    rest: true,
  });
  if (id === 't-claude')
    return [
      base('body', '人物、内搭与座椅', 'body'),
      ...cuts.filter((l) => l.source === 'body').reverse(),
      base('gown-bodice', '裙装上身与固定接缝', 'dress'),
      ...cuts.filter((l) => l.source === 'dress').reverse(),
      base('front', '手、书本与前景发丝', 'front'),
    ];
  return [
    base('body', '人物与固定部件', 'hero'),
    ...cuts.reverse(),
    ...(id === 't-gemini'
      ? [
          {
            name: 'held-star',
            label: '悬挂星饰与蝴蝶结',
            source: 'prop' as const,
            rect: [268, 295, 110, 144] as const,
            fixed: [],
            rest: true,
            rigid: { pivot: [320, 299] as const, radians: 0.026, period: 8.8 },
          },
        ]
      : []),
  ];
}

function distance(points: readonly Point[], [x, y]: Point) {
  let inside = false,
    nearest = Infinity;
  for (let i = 0, j = points.length - 1; i < points.length; j = i++) {
    const a = points[j],
      b = points[i],
      dx = b[0] - a[0],
      dy = b[1] - a[1];
    if (a[1] > y !== b[1] > y && x < ((b[0] - a[0]) * (y - a[1])) / (b[1] - a[1]) + a[0])
      inside = !inside;
    const t = Math.max(
      0,
      Math.min(1, ((x - a[0]) * dx + (y - a[1]) * dy) / (dx * dx + dy * dy || 1)),
    );
    nearest = Math.min(nearest, Math.hypot(x - a[0] - t * dx, y - a[1] - t * dy));
  }
  return inside ? nearest : -nearest;
}
const smooth = (a: number, b: number, x: number) => {
  const t = Math.max(0, Math.min(1, (x - a) / (b - a)));
  return t * t * (3 - 2 * t);
};
/** Narrow stationary seams sit inside the wider motion feather. */
export function cutWeight(layer: ArtLayer, point: Point) {
  if (!layer.mask) return 0;
  let weight = smooth(0, 5, distance(layer.mask, point));
  for (const fixed of layer.fixed)
    weight = Math.min(weight, smooth(8, 14, -distance(fixed, point)));
  if (layer.region) {
    const { root, tip } = layer.region,
      dx = tip[0] - root[0],
      dy = tip[1] - root[1];
    weight *= smooth(
      0,
      0.12,
      ((point[0] - root[0]) * dx + (point[1] - root[1]) * dy) / (dx * dx + dy * dy),
    );
  }
  return weight;
}
