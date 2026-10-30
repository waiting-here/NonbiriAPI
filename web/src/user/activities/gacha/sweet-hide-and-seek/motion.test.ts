import { expect, it } from 'vitest';
import { createMotionMesh, vertexStride } from './motion';
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

it.each(['s-chatgpt', 's-claude'] as const)(
  '%s expanded mesh retains every original vertex and weight while extending edge motion',
  (id) => {
    const original = createMotionMesh(id);
    const expanded = createMotionMesh(id, {
      canvas: [1552, 1808],
      originalRegion: { x: 264, y: 272, width: 1024, height: 1536 },
    });
    const byPosition = new Map<string, number>();
    for (let i = 0; i < expanded.vertices.length; i += vertexStride)
      byPosition.set(expanded.vertices[i] + ',' + expanded.vertices[i + 1], i);
    for (let i = 0; i < original.vertices.length; i += vertexStride) {
      const offset = byPosition.get(original.vertices[i] + ',' + original.vertices[i + 1])!;
      expect(Array.from(expanded.vertices.slice(offset, offset + vertexStride))).toEqual(
        Array.from(original.vertices.slice(i, i + vertexStride)),
      );
    }
    const originalEdge = byPosition.get('1024,800')!;
    const addedEdge = byPosition.get('1288,800')!;
    expect(Array.from(expanded.vertices.slice(addedEdge + 2, addedEdge + vertexStride))).toEqual(
      Array.from(expanded.vertices.slice(originalEdge + 2, originalEdge + vertexStride)),
    );
    expect(byPosition.has('-264,-272')).toBe(true);
    expect(byPosition.has('1288,1536')).toBe(true);
    expect(expanded.indices.every((index) => index < expanded.vertices.length / vertexStride)).toBe(
      true,
    );
    expect(expanded.vertices.length / vertexStride).toBeLessThan(65536);
  },
);
