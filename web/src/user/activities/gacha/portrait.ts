export type PolishTheme = 'mirrorsea' | 'amber' | 'elopement' | 'heart-thief';

// The portrait is deliberately closer; the landscape retains the complete outfit.
export const portraitWindows = {
  mirrorsea: [0.115, 0.082, 0.77, 0.77],
  amber: [0.065, 0.025, 0.87, 0.87],
  elopement: [0.13, 0.027, 0.75, 0.75],
  'heart-thief': [0.13, 0.025, 0.75, 0.75],
} as const;

export function portraitView(view: readonly number[], theme: PolishTheme) {
  const [x, y, width, height] = portraitWindows[theme];
  return [view[0] + x * view[2], view[1] + y * view[3], width * view[2], height * view[3]];
}
