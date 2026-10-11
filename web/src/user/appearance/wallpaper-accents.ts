import type { Rect, WallpaperTheme } from './wallpaper-animation';

export type Accent = { rect: Rect; opacity: number };

// Small highlights follow the physical hanging decorations, not the distant sky.
const ornaments: Record<WallpaperTheme, [number, number][]> = {
  light: [
    [0.16, 0.059],
    [0.257, 0.082],
    [0.381, 0.205],
    [0.648, 0.229],
    [0.812, 0.197],
    [0.828, 0.032],
    [0.731, 0.04],
  ],
  dark: [
    [0.563, 0.039],
    [0.768, 0.073],
    [0.926, 0.091],
    [0.666, 0.186],
    [0.872, 0.302],
    [0.781, 0.329],
    [0.1, 0.164],
    [0.14, 0.073],
  ],
};

function glint(x: number, y: number, size: number, opacity: number): Accent {
  const height = (size * 16) / 9;
  return { rect: [x - size / 2, y - height / 2, size, height], opacity };
}

export function decorationGlints(t: number, theme: WallpaperTheme): Accent[] {
  const strength = theme === 'dark' ? 0.32 : 0.21;
  const result = ornaments[theme].map(([x, y], i) =>
    glint(x, y, 0.019, strength * Math.pow(0.5 + 0.5 * Math.sin(t * 0.7 - i * 0.87), 3)),
  );
  // A faint light travels along one of the long gold garlands.
  const u = (t / 23) % 1,
    v = 1 - u;
  const points =
    theme === 'dark'
      ? [
          [0.564, 0.02],
          [0.559, 0.366],
          [0.95, 0.37],
        ]
      : [
          [0.32, 0.18],
          [0.61, 0.33],
          [0.86, 0.17],
        ];
  const x = v * v * points[0][0] + 2 * u * v * points[1][0] + u * u * points[2][0];
  const y = v * v * points[0][1] + 2 * u * v * points[1][1] + u * u * points[2][1];
  result.push(glint(x, y, 0.011, strength * Math.sin(Math.PI * u) ** 2));
  return result;
}

/** Brief, infrequent meteors stay behind the opaque planets and architecture. */
export function meteorAccent(t: number): Accent | undefined {
  const cycle = t % 29;
  const starts = [5.5, 20.5];
  for (let i = 0; i < starts.length; i++) {
    const u = (cycle - starts[i]) / 1.9;
    if (u < 0 || u > 1) continue;
    const x = (i === 0 ? 0.44 : 0.68) - u * 0.19;
    const y = (i === 0 ? 0.055 : 0.16) + u * 0.11;
    return { rect: [x, y, 0.09, 0.061], opacity: 0.42 * Math.sin(Math.PI * u) ** 2 };
  }
  return undefined;
}
