export type EncoreCard = 'u-gemini' | 'u-glm' | 'u-deepseek';
export type EncoreLayer = {
  name: string;
  label: string;
  rect: readonly [number, number, number, number];
  anchor: readonly [number, number];
  axis: 'up' | 'down' | 'right';
  reach: number;
  amplitude: readonly [number, number];
  period: number;
  phase: number;
  depth?: 'behind' | 'front';
  exclusions?: readonly (readonly (readonly [number, number])[])[];
  seam?: readonly (readonly [number, number])[];
};
const hairExclusions: Record<EncoreCard, Record<string, NonNullable<EncoreLayer['exclusions']>>> = {
  'u-gemini': {
    'hair-left': [
      [
        [270, 700],
        [306, 689],
        [402, 700],
        [415, 850],
        [285, 870],
        [265, 795],
      ],
    ],
    'hair-right': [
      [
        [645, 235],
        [705, 233],
        [746, 300],
        [751, 492],
        [671, 498],
        [654, 430],
      ],
      [
        [686, 475],
        [726, 475],
        [728, 815],
        [686, 815],
      ],
    ],
  },
  'u-glm': {
    'hair-left': [
      [
        [280, 708],
        [307, 739],
        [270, 803],
        [230, 816],
        [213, 884],
        [159, 887],
        [169, 818],
        [193, 773],
        [212, 746],
      ],
    ],
    'hair-right': [
      [
        [648, 320],
        [681, 340],
        [685, 449],
        [671, 505],
        [646, 504],
      ],
    ],
  },
  'u-deepseek': {
    'hair-left': [
      [
        [210, 280],
        [455, 280],
        [455, 396],
        [418, 419],
        [300, 422],
        [210, 370],
      ],
      [
        [349, 665],
        [405, 650],
        [450, 790],
        [347, 790],
      ],
    ],
    'hair-right': [
      [
        [596, 280],
        [850, 280],
        [850, 360],
        [763, 417],
        [632, 415],
        [596, 396],
      ],
      [
        [700, 721],
        [763, 725],
        [782, 790],
        [703, 790],
      ],
      [
        [875, 724],
        [920, 724],
        [920, 800],
        [875, 800],
      ],
    ],
  },
};
const hair = (
  id: EncoreCard,
  name: string,
  label: string,
  rect: EncoreLayer['rect'],
  anchor: EncoreLayer['anchor'],
  reach: number,
  amplitude: EncoreLayer['amplitude'],
  period: number,
  phase: number,
  seam: NonNullable<EncoreLayer['seam']>,
): EncoreLayer => ({
  exclusions: hairExclusions[id][name],
  name,
  label,
  rect,
  anchor,
  reach,
  amplitude,
  period,
  phase,
  seam,
  axis: 'down',
  depth: 'behind',
});
export const encoreLayers: Record<EncoreCard, readonly EncoreLayer[]> = {
  'u-gemini': [
    hair(
      'u-gemini',
      'hair-left',
      '左侧后发与卷梢',
      [96, 250, 290, 555],
      [359, 268],
      460,
      [13, 3.5],
      6.7,
      0,
      [
        [359, 268],
        [354, 330],
        [330, 425],
        [315, 560],
        [323, 625],
        [291, 706],
        [307, 760],
      ],
    ),
    hair(
      'u-gemini',
      'hair-right',
      '右侧后发与卷梢',
      [640, 236, 292, 575],
      [674, 270],
      470,
      [12, 3],
      7.9,
      1.1,
      [
        [674, 270],
        [718, 364],
        [746, 420],
        [738, 485],
        [754, 535],
        [720, 579],
        [680, 626],
        [650, 694],
        [651, 790],
      ],
    ),
  ],
  'u-glm': [
    hair(
      'u-glm',
      'hair-left',
      '左侧黑蓝后发',
      [78, 290, 300, 590],
      [352, 310],
      500,
      [12, 3],
      7.3,
      0.3,
      [
        [352, 310],
        [345, 420],
        [323, 530],
        [307, 630],
        [264, 728],
        [219, 764],
        [196, 850],
      ],
    ),
    hair(
      'u-glm',
      'hair-right',
      '右侧黑蓝后发',
      [642, 300, 342, 640],
      [660, 316],
      530,
      [14, 3.5],
      8.3,
      1.4,
      [
        [660, 316],
        [674, 504],
        [735, 586],
        [860, 660],
        [881, 760],
        [881, 855],
        [859, 925],
      ],
    ),
    {
      name: 'ribbon-left-free',
      label: '左侧缎带末端',
      rect: [94, 900, 158, 452],
      anchor: [178, 920],
      axis: 'down',
      reach: 420,
      amplitude: [10, 2],
      period: 5.2,
      phase: 0,
    },
    {
      name: 'ribbon-right-free',
      label: '右侧缎带末端',
      rect: [250, 905, 120, 335],
      anchor: [309, 930],
      axis: 'down',
      reach: 300,
      amplitude: [7, 1.8],
      period: 5.2,
      phase: 0.55,
    },
  ],
  'u-deepseek': [
    hair(
      'u-deepseek',
      'hair-left',
      '左侧海蓝后发',
      [185, 380, 250, 390],
      [401, 398],
      320,
      [11, 3],
      6.3,
      0.6,
      [
        [401, 398],
        [420, 451],
        [398, 535],
        [387, 579],
        [354, 630],
        [355, 740],
      ],
    ),
    hair(
      'u-deepseek',
      'hair-right',
      '右侧海蓝后发',
      [597, 378, 301, 398],
      [650, 399],
      320,
      [12, 3],
      7.1,
      1.8,
      [
        [650, 399],
        [646, 471],
        [649, 541],
        [625, 580],
        [599, 620],
        [612, 684],
        [641, 748],
      ],
    ),
    {
      name: 'ears-free',
      label: '兔耳',
      rect: [220, 0, 480, 114],
      anchor: [486, 112],
      axis: 'up',
      reach: 105,
      amplitude: [6, 1.2],
      period: 4.8,
      phase: 0,
    },
    {
      name: 'tail-fin',
      label: '鲸尾自由鳍',
      rect: [806, 640, 184, 250],
      anchor: [850, 840],
      axis: 'right',
      reach: 115,
      amplitude: [1.5, 6],
      period: 5.7,
      phase: 0.8,
    },
  ],
};
export function layerWeight(layer: EncoreLayer, x: number, y: number) {
  const distance =
    layer.axis === 'up'
      ? layer.anchor[1] - y
      : layer.axis === 'down'
        ? y - layer.anchor[1]
        : x - layer.anchor[0];
  const t = Math.max(0, Math.min(1, distance / layer.reach));
  let seam = 1;
  const lines = [
    ...(layer.seam ? [layer.seam] : []),
    ...(layer.exclusions ?? []).map((p) => [...p, p[0]]),
  ];
  if (lines.length) {
    let distance = Infinity;
    for (const line of lines)
      for (let i = 1; i < line.length; i++) {
        const a = line[i - 1],
          b = line[i],
          dx = b[0] - a[0],
          dy = b[1] - a[1];
        const q = Math.max(
          0,
          Math.min(1, ((x - a[0]) * dx + (y - a[1]) * dy) / (dx * dx + dy * dy)),
        );
        distance = Math.min(distance, Math.hypot(x - a[0] - q * dx, y - a[1] - q * dy));
      }
    const q = Math.max(0, Math.min(1, (distance - 4) / 72));
    seam = q * q * (3 - 2 * q);
  }
  return t * t * (3 - 2 * t) * seam;
}

export function layerPhase(layer: EncoreLayer, time: number) {
  return (
    (time * Math.PI * 2) / layer.period +
    layer.phase +
    0.22 * Math.sin((time * Math.PI * 2) / 29.3) +
    0.08 * Math.sin((time * Math.PI * 2) / 71.9 + layer.phase)
  );
}

export function layerOffset(layer: EncoreLayer, time: number, x: number, y: number) {
  const weight = layerWeight(layer, x, y);
  if (weight === 0) return [0, 0] as const;
  const phase = layerPhase(layer, time) - weight * 0.7;
  return [
    weight * layer.amplitude[0] * Math.sin(phase),
    weight * layer.amplitude[1] * Math.sin(phase + 0.4),
  ] as const;
}
