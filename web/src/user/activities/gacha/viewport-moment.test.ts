import { expect, it } from 'vitest';
import { viewportMomentLayout } from './viewport-moment';
import type { MomentScene } from './moment-frame';

const scene: MomentScene = {
  path: 'backgrounds/u.webp',
  canvas: [1672, 941],
  originalRegion: { x: 0, y: 0, width: 1024, height: 1536 },
  viewport: { placement: { bounds: [90, 0, 1000, 1505], anchor: [512, 1495] } },
};
it.each([
  [1920, 1080],
  [1920, 600],
  [1536, 480],
  [390, 844],
  [844, 390],
])(
  'covers %s×%s once and keeps the independent figure below navigation and on the floor',
  (w, h) => {
    const { background, character: c } = viewportMomentLayout(scene, w, h);
    expect(background.left).toBeLessThanOrEqual(0);
    expect(background.top).toBeLessThanOrEqual(0);
    expect(background.left + background.width).toBeGreaterThanOrEqual(w - 0.01);
    expect(background.top + background.height).toBeCloseTo(h);
    const scale = c.width / 1024;
    expect(c.top).toBeGreaterThanOrEqual(71.99);
    expect(c.left + 90 * scale).toBeGreaterThanOrEqual(-0.01);
    expect(c.left + 1000 * scale).toBeLessThanOrEqual(w + 0.01);
    expect(c.top + 1505 * scale).toBeLessThanOrEqual(h);
    expect(c.top + 1495 * scale).toBeCloseTo(h * 0.95);
  },
);
it('keeps an architectural scene and both character contact points on the same camera', () => {
  const door: MomentScene = {
    path: 'moment/s-claude/viewport-background.webp',
    canvas: [6144, 2048],
    originalRegion: { x: 5110, y: 350, width: 1024, height: 1536 },
    viewport: {},
  };
  for (const [w, h] of [
    [1920, 600],
    [1536, 480],
    [390, 844],
  ]) {
    const { background: b, character: c } = viewportMomentLayout(door, w, h);
    const s = b.width / 6144;
    expect(c.top).toBeGreaterThanOrEqual(71.99);
    expect(b.top).toBeLessThanOrEqual(0);
    expect(b.top + b.height).toBeGreaterThanOrEqual(h - 0.01);
    for (const [x, y] of [
      [680, 250],
      [450, 1500],
    ]) {
      expect(c.left + (x * c.width) / 1024).toBeCloseTo(b.left + (5110 + x) * s);
      expect(c.top + (y * c.height) / 1536).toBeCloseTo(b.top + (350 + y) * s);
    }
  }
});
it('fits the asymmetric DeepSeek silhouette on a phone', () => {
  const deepseek: MomentScene = {
    ...scene,
    viewport: { placement: { bounds: [209, 8, 958, 1463], anchor: [512, 1454] } },
  };
  const { character: c } = viewportMomentLayout(deepseek, 390, 844);
  expect(c.left + (209 * c.width) / 1024).toBeGreaterThanOrEqual(0);
  expect(c.left + (958 * c.width) / 1024).toBeLessThanOrEqual(390);
});
