import { expect, it } from 'vitest';
import { motionOffset, motionRegions, regionWeight } from './motion';
import type { SweetCard } from './motion';
import { eyeTextures, decodeMask, faceCrops } from './eyelid';

it('covers the amber iris pixels omitted from the input guide masks', () => {
  const { eyes } = eyeTextures('s-claude');
  for (const [i, x, y] of [
    [0, 552, 166],
    [1, 608, 143],
  ]) {
    const eye = eyes[i],
      [x0, y0, x1] = eye.maskBox;
    expect(decodeMask(eye)[(y - y0) * (x1 - x0) + x - x0]).toBe(255);
  }
});

it('keeps the foreshortened far-eye ribbon on actual ink and retains its wider outer corner', () => {
  const eye = eyeTextures('s-claude').eyes[1];
  expect(eye.lashSpans[0][0]).toBeGreaterThan(10);
  expect(eye.lashSpans[0][1]).toBeGreaterThan(36);
  expect(eye.lashSpans[2][0]).toBeLessThan(eye.lashSpans[0][0]);
  expect(eye.lashSpans[2][1]).toBeLessThan(eye.lashSpans[0][1]);
  const sample = (t: number) => {
    const x = ((t - eye.span[0]) / (eye.span[1] - eye.span[0])) * 63;
    const index = Math.floor(x),
      f = x - index;
    return eye.ribbons[1][index].map(
      (n, channel) => n * (1 - f) + eye.ribbons[1][index + 1][channel] * f,
    );
  };
  for (const t of [14, 18, 22, 26]) {
    expect(sample(t)[0]).toBeGreaterThan(-3);
    expect(sample(t)[0]).toBeLessThan(0.5);
  }
  const corner = sample(33),
    middle = sample(22);
  expect(corner[1] - corner[0]).toBeGreaterThan(6);
  expect(middle[1] - middle[0]).toBeLessThan(4);
});

it.each(['s-chatgpt', 's-claude'] as const)(
  '%s has no deformation outside the authored end polygons',
  (id) => {
    let moving = 0;
    for (let y = 0; y < 1536; y += 4)
      for (let x = 0; x < 1024; x += 4) {
        const weight = motionRegions[id].reduce(
          (sum, region) => sum + regionWeight(region, [x, y]),
          0,
        );
        const offset = motionOffset(id, [x, y], 2.4);
        if (weight === 0) expect(offset).toEqual([0, 0]);
        else {
          moving++;
          expect(Math.abs(offset[0])).toBeLessThanOrEqual(2.2);
          expect(Math.abs(offset[1])).toBeLessThanOrEqual(0.45);
        }
      }
    expect(moving).toBeGreaterThan(100);
  },
);

it('keeps whole protected body, contact and foreground rectangles fixed', () => {
  const fixed: Record<SweetCard, number[][]> = {
    's-chatgpt': [
      [0, 0, 1024, 390],
      [280, 380, 875, 1536],
      [150, 945, 280, 1110],
      [0, 1080, 1024, 1536],
    ],
    's-claude': [
      [325, 0, 1024, 550],
      [320, 550, 530, 1536],
      [525, 670, 670, 850],
      [0, 1320, 1024, 1536],
    ],
  };
  for (const id of Object.keys(fixed) as SweetCard[])
    for (const [x0, y0, x1, y1] of fixed[id]) {
      for (let y = y0; y < y1; y += 5)
        for (let x = x0; x < x1; x += 5) expect(motionOffset(id, [x, y], 3.1)).toEqual([0, 0]);
    }
});

it.each(['s-chatgpt', 's-claude'] as const)(
  '%s eye atlas stays registered and contains full masks and all three states',
  (id) => {
    const data = eyeTextures(id),
      face = faceCrops[id];
    expect(data.profiles.length).toBe(64 * 8 * 4);
    for (const eye of data.eyes) {
      expect(Math.hypot(...eye.axis)).toBeCloseTo(1, 6);
      const [x0, y0, x1, y1] = eye.maskBox;
      expect(x0).toBeGreaterThan(face[0]);
      expect(y0).toBeGreaterThan(face[1]);
      expect(x1).toBeLessThan(face[0] + face[2]);
      expect(y1).toBeLessThan(face[1] + face[3]);
      const mask = decodeMask(eye);
      expect(mask.length).toBe((x1 - x0) * (y1 - y0));
      expect(mask.some((n) => n === 0)).toBe(true);
      expect(mask.some((n) => n === 255)).toBe(true);
      expect(eye.ribbons).toHaveLength(3);
      for (const state of eye.ribbons)
        for (const [top, bottom] of state) expect(bottom).toBeGreaterThan(top);
    }
  },
);
