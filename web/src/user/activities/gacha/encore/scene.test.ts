import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { expect, it } from 'vitest';
import { stageLightingAt } from './stage-lighting';
import { partyRibbonsAt } from './party-ribbons';
import { encoreLayers, layerOffset, layerWeight } from './layers';
import { encoreEyelids } from './eyelids';
import type { EncoreCard } from './layers';

it('launches finite party ribbons, leaves quiet intervals and can seek without accumulated debris', () => {
  expect(partyRibbonsAt(0, 'u-gemini')).toEqual([]);
  expect(partyRibbonsAt(11, 'u-gemini').length).toBeGreaterThan(15);
  expect(partyRibbonsAt(25, 'u-gemini')).toEqual([]);
  expect(partyRibbonsAt(60, 'u-gemini')).toEqual([]);
  const early = partyRibbonsAt(11, 'u-gemini');
  for (const t of [48, 163.2, 1000, 3600, 100000]) {
    const particles = partyRibbonsAt(t, 'u-gemini');
    expect(particles.length).toBeLessThanOrEqual(52);
    for (const p of particles) {
      expect(p.alpha).toBeGreaterThan(0);
      expect(p.alpha).toBeLessThanOrEqual(1);
      expect(Number.isFinite(p.x + p.y)).toBe(true);
    }
  }
  expect(partyRibbonsAt(11, 'u-gemini')).toEqual(early);
});

it('keeps every stage cue bounded, smooth and free of a common short reset', () => {
  for (const id of Object.keys(encoreLayers) as EncoreCard[]) {
    for (const time of [0, 31.99, 32, 60, 180, 3600, 100000]) {
      const a = stageLightingAt(time, id),
        b = stageLightingAt(time + 1 / 60, id);
      for (let i = 0; i < a.beams.length; i++) {
        expect(a.beams[i].target[0]).toBeGreaterThan(100);
        expect(a.beams[i].target[0]).toBeLessThan(900);
        expect(a.beams[i].alpha).toBeGreaterThan(0);
        expect(a.beams[i].alpha).toBeLessThan(0.3);
        expect(Math.abs(a.beams[i].target[0] - b.beams[i].target[0])).toBeLessThan(2);
        expect(Math.abs(a.beams[i].alpha - b.beams[i].alpha)).toBeLessThan(0.002);
      }
      expect(stageLightingAt(time + 32, id)).not.toEqual(a);
      expect(stageLightingAt(time + 60, id)).not.toEqual(a);
      expect(a.tintStrength).toBeLessThan(0.04);
    }
  }
});

it('pins the attachment and keeps every accessory mesh continuous without folding', () => {
  for (const layers of Object.values(encoreLayers))
    for (const layer of layers) {
      expect(layerWeight(layer, ...layer.anchor)).toBe(0);
      const [x, y, w, h] = layer.rect;
      let minimum = Infinity;
      for (let t = 0; t < 36; t += 0.31) {
        expect(layerOffset(layer, t, ...layer.anchor)).toEqual([0, 0]);
        for (let py = y; py < y + h; py += 12)
          for (let px = x; px < x + w; px += 12) {
            const p = layerOffset(layer, t, px, py),
              right = layerOffset(layer, t, px + 1, py),
              down = layerOffset(layer, t, px, py + 1);
            const determinant =
              (1 + right[0] - p[0]) * (1 + down[1] - p[1]) - (down[0] - p[0]) * (right[1] - p[1]);
            minimum = Math.min(minimum, determinant);
          }
      }
      expect(minimum, layer.name).toBeGreaterThan(layer.depth === 'behind' ? 0.6 : 0.8);
    }
});

it('loads registered standard and large layers with complete per-eye coverage data', () => {
  for (const id of Object.keys(encoreLayers) as EncoreCard[]) {
    for (const eye of encoreEyelids[id]) {
      expect(eye.rows.map((row) => row.length)).toEqual([192, 192]);
      for (let col = 0; col < 48; col++) {
        const [top, bottom, upper, lower] = eye.rows[0].slice(col * 4, col * 4 + 4);
        expect(top).toBeLessThan(bottom);
        expect(upper).toBeLessThan(lower);
        expect(eye.rows[1][col * 4]).toBeLessThan(eye.rows[1][col * 4 + 1]);
      }
    }
    for (const high of ['', 'hires/']) {
      for (const part of [
        'hero',
        'closed',
        'eye-skin',
        'background',
        'parts/body',
        ...encoreLayers[id].map((p) => 'parts/' + p.name),
      ]) {
        const path = resolve('public/assets/gacha', high + 'motion/' + id + '/' + part + '.webp');
        expect(readFileSync(path).subarray(8, 12).toString(), path).toBe('WEBP');
      }
    }
  }
});
