export type Rect = [number, number, number, number];
export type WallpaperScene = 'celestial' | 'wandering';
export type WallpaperTheme = 'light' | 'dark';

export type Portrait = { rect: Rect; eyes: Rect };
export const portraits: Record<WallpaperScene, Record<WallpaperTheme, Portrait>> = {
  wandering: {
    light: {
      rect: [0.245, 0.25, 0.8, 0.8],
      eyes: [0.739, 0.2, 0.775, 0.224],
    },
    dark: {
      rect: [0.35, 0.254, 0.75, 0.75],
      eyes: [0.663, 0.243, 0.714, 0.191],
    },
  },
  celestial: {
    light: {
      rect: [0.295, 0.277, 0.72, 0.72],
      eyes: [0.763, 0.16, 0.798, 0.181],
    },
    dark: {
      rect: [0.357, 0.338, 0.675, 0.675],
      eyes: [0.752, 0.154, 0.796, 0.185],
    },
  },
};

const smooth = (value: number) => {
  const t = Math.max(0, Math.min(1, value));
  return t * t * (3 - 2 * t);
};

/** A quick closure and slower release, with enough intermediate eyelid poses. */
export function blinkAt(seconds: number): number {
  const cycle = seconds % 17.6;
  for (const start of [2.2, 7.4, 13.1]) {
    const t = cycle - start;
    if (t >= 0 && t < 0.13) return smooth(t / 0.13);
    if (t >= 0.13 && t < 0.175) return 1;
    if (t >= 0.175 && t < 0.415) return 1 - smooth((t - 0.175) / 0.24);
  }
  return 0;
}

/** Keep the fractional frame budget instead of rounding every frame up to a vsync. */
export class PaintCadence {
  private next = -Infinity;
  constructor(private readonly interval = 1000 / 60) {}
  reset() {
    this.next = -Infinity;
  }
  due(now: number): boolean {
    if (now + 0.5 < this.next) return false;
    if (!Number.isFinite(this.next) || now - this.next > this.interval * 2)
      this.next = now + this.interval;
    else this.next += this.interval;
    return true;
  }
}

export function whaleRect(t: number, width: number, height: number): Rect {
  const size = width < 640 ? 1.55 : 0.78;
  const x = 1.18 - ((t / 84 + 0.19) % 1) * (2.36 + size);
  const h = (((size * width) / height) * 9) / 16;
  return [x, 0.025 + 0.014 * Math.sin(t * 0.19), size, h];
}

/** Distant bodies follow long, continuous arcs, retaining their rigid silhouette. */
export function celestialRects(t: number): [Rect, Rect] {
  const angle = (t * Math.PI * 2) / 420 + 0.5;
  const x = 0.055 * Math.cos(angle),
    y = 0.027 * Math.sin(angle);
  const moonSize = 0.215,
    earthSize = 0.14;
  return [
    [0.74 + x - moonSize / 2, 0.25 + y - (moonSize * 8) / 9, moonSize, (moonSize * 16) / 9],
    [0.48 + x - earthSize / 2, 0.17 + y - (earthSize * 8) / 9, earthSize, (earthSize * 16) / 9],
  ];
}

// These boxes enclose individual waterfall faces, not the surrounding scenery.
export const waterfalls: Record<WallpaperScene, Record<WallpaperTheme, Rect[]>> = {
  celestial: {
    light: [[0.736, 0.532, 0.02, 0.11]],
    dark: [
      [0.134, 0.744, 0.025, 0.069],
      [0.183, 0.749, 0.029, 0.075],
    ],
  },
  wandering: {
    light: [
      [0.062, 0.137, 0.013, 0.059],
      [0.038, 0.327, 0.017, 0.1],
      [0.133, 0.676, 0.026, 0.098],
      [0.72, 0.702, 0.028, 0.085],
    ],
    dark: [
      [0.038, 0.626, 0.019, 0.089],
      [0.58, 0.302, 0.013, 0.034],
      [0.708, 0.165, 0.015, 0.044],
    ],
  },
};
