import type { MomentFrame } from '../moment-frame';
import { motionRegions } from './motion';
import type { MotionRegion, Point, SweetCard } from './motion';

export type SweetLayer = {
  name: string;
  label: string;
  polygon?: readonly Point[];
  material?: MotionRegion;
  rigid?: { pivot: Point; angle: number; period: number };
  fixed?: boolean;
  veil?: boolean;
  protectAnatomy?: boolean;
  rest?: boolean;
};
export type LayerRect = readonly [number, number, number, number];
const material = (
  name: string,
  polygon: Point[],
  root: Point,
  tip: Point,
  amplitude: Point,
): MotionRegion => ({
  name,
  polygon,
  root,
  tip,
  amplitude,
  phase: -0.4,
  feather: 24,
});
const props: Record<SweetCard, readonly SweetLayer[]> = {
  's-chatgpt': [
    {
      name: 'mouth-wrapper',
      label: '口边糖纸',
      polygon: [
        [439, 450],
        [467, 452],
        [492, 474],
        [486, 501],
        [461, 510],
        [436, 492],
      ],
      material: material(
        'mouth-wrapper',
        [
          [433, 444],
          [496, 445],
          [501, 515],
          [430, 516],
        ],
        [475, 464],
        [455, 501],
        [2.2, 1.3],
      ),
    },
    {
      name: 'neck-pendant',
      label: '颈链与翡翠坠',
      polygon: [
        [528, 548],
        [550, 549],
        [554, 582],
        [562, 610],
        [560, 646],
        [547, 665],
        [530, 649],
        [523, 622],
        [529, 586],
      ],
      rigid: { pivot: [539, 551], angle: 0.025, period: 6.1 },
    },
    {
      name: 'hat-ribbon',
      label: '帽带末端',
      polygon: [
        [551, 123],
        [586, 129],
        [611, 168],
        [631, 217],
        [592, 222],
        [570, 189],
      ],
      material: material(
        'hat-ribbon',
        [
          [545, 118],
          [593, 118],
          [642, 224],
          [583, 230],
        ],
        [567, 130],
        [614, 212],
        [3.2, 1.7],
      ),
    },
    {
      name: 'hat-star',
      label: '帽尖星扣',
      polygon: [
        [702, 51],
        [737, 43],
        [757, 68],
        [784, 84],
        [767, 109],
        [744, 143],
        [713, 126],
        [691, 98],
      ],
      rigid: { pivot: [722, 57], angle: 0.018, period: 7.3 },
    },
    {
      name: 'lower-wrapper',
      label: '指间糖纸',
      polygon: [
        [155, 1061],
        [180, 1053],
        [194, 1076],
        [184, 1105],
        [163, 1107],
        [149, 1088],
      ],
      material: material(
        'lower-wrapper',
        [
          [143, 1047],
          [200, 1047],
          [200, 1114],
          [143, 1114],
        ],
        [183, 1064],
        [163, 1095],
        [2.4, 1.5],
      ),
    },
  ],
  's-claude': [
    {
      name: 'hair-tassels',
      label: '发饰流苏',
      polygon: [
        [351, 183],
        [403, 185],
        [422, 224],
        [414, 280],
        [381, 301],
        [342, 274],
        [320, 252],
      ],
      material: material(
        'hair-tassels',
        [
          [311, 175],
          [426, 176],
          [431, 310],
          [310, 310],
        ],
        [380, 190],
        [366, 281],
        [3.2, 1.5],
      ),
    },
    {
      name: 'hair-bow-tail',
      label: '发饰缎带',
      polygon: [
        [358, 143],
        [392, 150],
        [408, 180],
        [390, 209],
        [357, 201],
        [337, 185],
      ],
      material: material(
        'hair-bow-tail',
        [
          [332, 137],
          [410, 137],
          [418, 216],
          [330, 216],
        ],
        [382, 154],
        [351, 195],
        [2.3, 1.2],
      ),
    },
    {
      name: 'candy-wrapper',
      label: '掌下糖纸',
      polygon: [
        [551, 822],
        [574, 820],
        [588, 843],
        [582, 860],
        [558, 854],
        [545, 844],
      ],
      material: material(
        'candy-wrapper',
        [
          [539, 811],
          [591, 811],
          [594, 866],
          [539, 866],
        ],
        [566, 824],
        [566, 853],
        [2.5, 1.6],
      ),
    },
    {
      name: 'sleeve-bow-tail',
      label: '袖结垂带',
      polygon: [
        [550, 445],
        [566, 446],
        [572, 472],
        [578, 512],
        [564, 508],
        [558, 515],
        [549, 480],
      ],
      material: material(
        'sleeve-bow-tail',
        [
          [545, 441],
          [571, 441],
          [583, 518],
          [548, 519],
        ],
        [558, 448],
        [567, 510],
        [2.5, 1.1],
      ),
    },
  ],
};

/** Every cut has one driver. Fitted cloth, support hands and feet remain on the body plate. */
export function sweetCuts(id: SweetCard, frame?: MomentFrame): readonly SweetLayer[] {
  const cuts: SweetLayer[] = [
    ...props[id],
    ...motionRegions[id]
      .slice()
      .sort((a, b) => {
        const priority = (name: string) =>
          name.includes('sleeve') || name.includes('hem') ? 0 : name === 'outer-curls' ? 1 : 2;
        return priority(a.name) - priority(b.name);
      })
      .map((region) => ({
        name: region.name,
        label: (
          {
            'left-hair': '左侧发束',
            'right-hair': '右侧发束',
            'back-hair': '后发',
            'outer-curls': '外侧卷发',
            'left-sleeve': '左侧袖纱',
            'right-sleeve': '右侧袖纱',
            'left-hem': '左侧裙纱',
            'right-hem': '右侧裙纱',
          } as Record<string, string>
        )[region.name],
        polygon: region.polygon,
        material: region,
        protectAnatomy: true,
        veil: region.name.includes('hem') || region.name.includes('sleeve'),
      })),
    ...(id === 's-chatgpt'
      ? [
          {
            name: 'candy-foreground',
            label: '前景糖堆与接触影',
            polygon: [
              [-384, 1117],
              [170, 1117],
              [183, 1182],
              [214, 1267],
              [251, 1356],
              [300, 1430],
              [361, 1514],
              [411, 1545],
              [650, 1545],
              [675, 1485],
              [720, 1400],
              [753, 1288],
              [776, 1180],
              [816, 1117],
              [1408, 1117],
              [1408, 1792],
              [-384, 1792],
            ] as Point[],
            fixed: true,
          },
        ]
      : []),
  ];
  if (!frame) return cuts;
  const map = (p: Point): Point => [
    p[0] < 0
      ? p[0] - frame.originalRegion.x
      : p[0] > 1024
        ? p[0] + frame.canvas[0] - frame.originalRegion.x - 1024
        : p[0],
    p[1] < 0 ? p[1] - frame.originalRegion.y : p[1],
  ];
  return cuts.map((layer) => ({
    ...layer,
    polygon: layer.polygon?.map(map),
    material: layer.material && { ...layer.material, polygon: layer.material.polygon.map(map) },
  }));
}
export function sweetLayers(id: SweetCard, frame?: MomentFrame): readonly SweetLayer[] {
  return [
    { name: 'body', label: '人物与贴身衣料', rest: true },
    ...sweetCuts(id, frame).slice().reverse(),
  ];
}
export function layerRect(layer: SweetLayer, frame?: MomentFrame): LayerRect {
  const ox = frame?.originalRegion.x ?? 0,
    oy = frame?.originalRegion.y ?? 0;
  const width = frame?.canvas[0] ?? 1024,
    height = frame?.canvas[1] ?? 1536;
  if (layer.rest) return [-ox, -oy, width, height];
  const points = layer.polygon!;
  const x = Math.max(-ox, Math.floor(Math.min(...points.map((p) => p[0])) - 28));
  const y = Math.max(-oy, Math.floor(Math.min(...points.map((p) => p[1])) - 28));
  return [
    x,
    y,
    Math.min(width - ox, Math.ceil(Math.max(...points.map((p) => p[0])) + 28)) - x,
    Math.min(height - oy, Math.ceil(Math.max(...points.map((p) => p[1])) + 28)) - y,
  ];
}
