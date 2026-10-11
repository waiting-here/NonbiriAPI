import type { WallpaperScene, WallpaperTheme } from './wallpaper-animation';

type Point = readonly [number, number];
type Region = { outline: readonly Point[]; root: Point; tip: Point };
type Rig = { hair: readonly Region[]; cloth: readonly Region[] };
type PortraitID = `${WallpaperScene}-${WallpaperTheme}`;

// Only detached hair and trailing fabric are flexible. The head, torso,
// limbs and fitted clothing stay outside these authored regions.
export const portraitRigs: Record<PortraitID, Rig> = {
  'wandering-light': {
    hair: [
      {
        outline: [
          [0.83, 0.3],
          [0.9, 0.24],
          [1.04, 0.25],
          [1.04, 0.61],
          [0.91, 0.53],
          [0.84, 0.4],
        ],
        root: [0.84, 0.3],
        tip: [1, 0.46],
      },
      {
        outline: [
          [0.72, 0.28],
          [0.69, 0.34],
          [0.665, 0.4],
          [0.65, 0.49],
          [0.59, 0.51],
          [0.57, 0.37],
          [0.66, 0.28],
        ],
        root: [0.72, 0.28],
        tip: [0.61, 0.45],
      },
    ],
    cloth: [
      {
        outline: [
          [0.79, 0.59],
          [0.89, 0.54],
          [1.04, 0.59],
          [1.04, 1.04],
          [0.9, 0.97],
          [0.86, 0.84],
        ],
        root: [0.79, 0.6],
        tip: [1, 0.9],
      },
      {
        outline: [
          [0.64, 0.78],
          [0.69, 0.85],
          [0.78, 0.96],
          [0.89, 1.05],
          [0.59, 1.05],
          [0.58, 0.84],
        ],
        root: [0.65, 0.79],
        tip: [0.7, 1],
      },
    ],
  },
  'wandering-dark': {
    hair: [
      {
        outline: [
          [0.61, 0.28],
          [0.65, 0.35],
          [0.62, 0.47],
          [0.55, 0.55],
          [0.39, 0.58],
          [0.34, 0.43],
          [0.4, 0.3],
          [0.53, 0.24],
        ],
        root: [0.62, 0.29],
        tip: [0.43, 0.48],
      },
      {
        outline: [
          [0.77, 0.26],
          [0.85, 0.23],
          [0.87, 0.31],
          [0.83, 0.36],
          [0.79, 0.33],
        ],
        root: [0.77, 0.27],
        tip: [0.85, 0.3],
      },
    ],
    cloth: [
      {
        outline: [
          [0.6, 0.54],
          [0.63, 0.68],
          [0.58, 0.87],
          [0.43, 1.04],
          [0.04, 1.04],
          [0.13, 0.81],
          [0.22, 0.5],
          [0.4, 0.49],
        ],
        root: [0.63, 0.57],
        tip: [0.2, 0.83],
      },
      {
        outline: [
          [0.87, 0.39],
          [0.94, 0.34],
          [1.04, 0.36],
          [1.04, 0.55],
          [0.9, 0.55],
        ],
        root: [0.87, 0.4],
        tip: [1, 0.46],
      },
    ],
  },
  'celestial-light': {
    hair: [
      {
        outline: [
          [0.84, 0.23],
          [0.9, 0.21],
          [1.04, 0.28],
          [1.04, 0.51],
          [0.91, 0.46],
          [0.84, 0.36],
        ],
        root: [0.85, 0.24],
        tip: [0.96, 0.43],
      },
      {
        outline: [
          [0.73, 0.27],
          [0.73, 0.35],
          [0.68, 0.47],
          [0.64, 0.46],
          [0.62, 0.36],
          [0.68, 0.28],
        ],
        root: [0.73, 0.28],
        tip: [0.65, 0.43],
      },
    ],
    cloth: [
      {
        outline: [
          [0.85, 0.58],
          [0.94, 0.55],
          [1.04, 0.62],
          [1.04, 1.04],
          [0.86, 1.04],
          [0.8, 0.81],
        ],
        root: [0.85, 0.59],
        tip: [0.97, 0.9],
      },
      {
        outline: [
          [0.54, 0.66],
          [0.54, 0.73],
          [0.5, 0.83],
          [0.43, 0.94],
          [0.32, 0.9],
          [0.32, 0.76],
          [0.44, 0.67],
        ],
        root: [0.54, 0.69],
        tip: [0.37, 0.85],
      },
    ],
  },
  'celestial-dark': {
    hair: [
      {
        outline: [
          [0.85, 0.24],
          [0.93, 0.25],
          [1.04, 0.41],
          [1.04, 0.67],
          [0.88, 0.61],
          [0.84, 0.39],
        ],
        root: [0.85, 0.26],
        tip: [0.96, 0.54],
      },
      {
        outline: [
          [0.73, 0.28],
          [0.73, 0.35],
          [0.7, 0.43],
          [0.65, 0.46],
          [0.64, 0.38],
          [0.68, 0.3],
        ],
        root: [0.73, 0.28],
        tip: [0.67, 0.41],
      },
    ],
    cloth: [
      {
        outline: [
          [0.81, 0.68],
          [0.94, 0.61],
          [1.04, 0.65],
          [1.04, 1.04],
          [0.66, 1.04],
          [0.66, 0.85],
        ],
        root: [0.8, 0.68],
        tip: [0.95, 1],
      },
    ],
  },
};

const smooth = (v: number) => {
  const t = Math.max(0, Math.min(1, v));
  return t * t * (3 - 2 * t);
};
function weight(p: Point, region: Region): number {
  let inside = false,
    distance = Infinity;
  const points = region.outline;
  for (let i = 0, j = points.length - 1; i < points.length; j = i++) {
    const a = points[j],
      b = points[i];
    if (
      a[1] > p[1] !== b[1] > p[1] &&
      p[0] < ((b[0] - a[0]) * (p[1] - a[1])) / (b[1] - a[1]) + a[0]
    )
      inside = !inside;
    const dx = b[0] - a[0],
      dy = b[1] - a[1];
    const t = Math.max(
      0,
      Math.min(1, ((p[0] - a[0]) * dx + (p[1] - a[1]) * dy) / (dx * dx + dy * dy)),
    );
    distance = Math.min(distance, Math.hypot(p[0] - a[0] - t * dx, p[1] - a[1] - t * dy));
  }
  if (!inside) return 0;
  const dx = region.tip[0] - region.root[0],
    dy = region.tip[1] - region.root[1];
  const along = ((p[0] - region.root[0]) * dx + (p[1] - region.root[1]) * dy) / (dx * dx + dy * dy);
  return smooth(distance / 0.035) * smooth(along);
}
export function portraitFlex(id: PortraitID, p: Point): [number, number] {
  const rig = portraitRigs[id];
  return [
    Math.max(0, ...rig.hair.map((region) => weight(p, region))),
    Math.max(0, ...rig.cloth.map((region) => weight(p, region))),
  ];
}
