import { expect, it } from 'vitest';
import { displayDensity, graphicsLimit, layerDensity } from './artwork-playback';

it('preserves native non-16:9 and registered wide frames above eight megapixels', () => {
  for (const [width, height] of [
    [3840, 2160],
    [3840, 2560],
    [6144, 2048],
    [2560, 3840],
  ]) {
    const w = width / 2,
      h = height / 2;
    const native = layerDensity({ naturalWidth: width, naturalHeight: height }, w, h);
    expect(displayDensity(w, h, native, [8192, 8192], 3)).toBe(2);
    expect(displayDensity(w, h, native, [8192, 8192], 1.5)).toBe(1.5);
  }
});

it('observes both graphics viewport axes and the texture/renderbuffer limits', () => {
  const gl = {
    MAX_VIEWPORT_DIMS: 1,
    MAX_TEXTURE_SIZE: 2,
    MAX_RENDERBUFFER_SIZE: 3,
    getParameter: (p: number) => (p === 1 ? [8192, 3072] : p === 2 ? 4096 : 6144),
  } as unknown as WebGLRenderingContext;
  const limits = graphicsLimit(gl);
  expect(limits).toEqual([4096, 3072]);
  expect(displayDensity(1024, 2048, 3, limits, 3)).toBe(1.5);
  expect(displayDensity(3072, 1024, 2, limits, 3)).toBeCloseTo(4 / 3);
});
