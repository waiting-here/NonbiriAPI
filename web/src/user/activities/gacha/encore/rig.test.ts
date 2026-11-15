import { expect, it } from 'vitest';
import { bindEncorePoint, encorePoseAt, encoreRigs, posedEncorePoint } from './rig';
import { encoreLayers, layerOffset, type EncoreCard } from './layers';

const ids = Object.keys(encoreRigs) as EncoreCard[];
const delta = (a: readonly number[], b: readonly number[]) => Math.hypot(a[0] - b[0], a[1] - b[1]);
const point = (id: EncoreCard, time: number, x: number, y: number, body = true) =>
  posedEncorePoint(bindEncorePoint(id, x, y, body), encorePoseAt(id, time));

it('keeps the supplied resting pose and every planted support', () => {
  for (const id of ids) {
    for (let y = 0; y <= 1536; y += 32)
      for (let x = 0; x <= 1024; x += 32)
        expect(delta(point(id, 0, x, y), [x, y])).toBeLessThan(0.000001);
    for (const t of [1.5, 4.7, 20.1, 180.3, 3600]) {
      for (const [x, y] of [
        [450, 1450],
        [525, 1460],
      ])
        expect(point(id, t, x, y)).toEqual([x, y]);
      if (id === 'u-gemini')
        for (const [x, y] of [
          [708, 400],
          [705, 900],
          [705, 1400],
        ])
          expect(point(id, t, x, y)).toEqual([x, y]);
      if (id === 'u-glm')
        for (const [x, y] of [
          [244, 783],
          [720, 1000],
        ])
          expect(point(id, t, x, y)).toEqual([x, y]);
      if (id === 'u-deepseek')
        for (const [x, y] of [
          [405, 205],
          [618, 191],
        ])
          expect(delta(point(id, t, x, y), point(id, t, x, y, false))).toBeLessThan(0.01);
    }
  }
});

it('moves the chest, shoulders and unsupported arm without an elbow crease', () => {
  for (const id of ids) {
    const { neck, waist } = encoreRigs[id];
    const x = neck[0],
      y = neck[1] + (waist[1] - neck[1]) * 0.33;
    expect(delta(point(id, 1.5, x, y), [x, y])).toBeGreaterThan(1.5);
  }
  expect(delta(point('u-gemini', 2, 348, 741), [348, 741])).toBeGreaterThan(3);
  for (const [id, x, y] of [
    ['u-gemini', 668.155, 503.838],
    ['u-glm', 306.201, 569.39],
  ] as const) {
    expect(delta(point(id, 1.5, x - 0.001, y), point(id, 1.5, x + 0.001, y))).toBeLessThan(0.003);
  }
});

it('keeps actual indexed body and hair triangles unfolded at motion extremes', () => {
  let minimum = Infinity;
  let worst = {};
  for (const id of ids) {
    for (const layer of [undefined, ...encoreLayers[id]]) {
      const [x, y, w, h] = layer?.rect ?? [0, 0, 1024, 1536];
      const cols = Math.ceil(w / (layer ? 12 : 16)),
        rows = Math.ceil(h / (layer ? 12 : 16));
      const bindings = Array.from({ length: (cols + 1) * (rows + 1) }, (_, i) =>
        bindEncorePoint(
          id,
          x + ((i % (cols + 1)) * w) / cols,
          y + (Math.floor(i / (cols + 1)) * h) / rows,
          true,
        ),
      );
      for (const time of [0, 1.5, 3.2, 6.8, 11.9, 27, 71.9, 180, 3600]) {
        const pose = encorePoseAt(id, time);
        const points = bindings.map((binding) => {
          const p = posedEncorePoint(binding, pose),
            o = layer ? layerOffset(layer, time, ...binding.source) : [0, 0];
          return [p[0] + o[0], p[1] + o[1]];
        });
        for (let r = 0; r < rows; r++)
          for (let c = 0; c < cols; c++) {
            const a = r * (cols + 1) + c,
              b = a + 1,
              d = a + cols + 2,
              e = a + cols + 1;
            for (const [i, j, k] of [
              [a, b, e],
              [e, b, d],
            ]) {
              const [p, q, s] = [points[i], points[j], points[k]];
              const ratio =
                ((q[0] - p[0]) * (s[1] - p[1]) - (q[1] - p[1]) * (s[0] - p[0])) /
                ((w / cols) * (h / rows));
              if (ratio < minimum) {
                minimum = ratio;
                worst = { id, layer: layer?.name ?? 'body', time, source: bindings[i].source };
              }
            }
          }
      }
    }
  }
  expect(minimum, JSON.stringify(worst)).toBeGreaterThan(0.35);
});
