import { expect, it } from 'vitest';
import { backgroundRenderSize } from './background-size';
import { romanceBackdropPlacement } from './layout';

it('preserves the standard background while bounding high output to projected pixels', () => {
  for (const [width, height, viewport, expected] of [
    [1440, 900, 1440, [1536, 1024]],
    [2560, 1600, 1440, [2714, 1809]],
    [1170, 2532, 390, [3798, 2532]],
  ] as const) {
    const projection = romanceBackdropPlacement(width, height, viewport, 'heart-thief');
    expect(backgroundRenderSize(3840, 2560, projection.scale)).toEqual(expected);
    expect(backgroundRenderSize(1536, 1024, projection.scale)).toEqual([1536, 1024]);
  }
});

it('can shrink after a larger view and never exceeds the source texture', () => {
  expect([1, 2, 5, 1].map((scale) => backgroundRenderSize(3840, 2560, scale))).toEqual([
    [1536, 1024],
    [3072, 2048],
    [3840, 2560],
    [1536, 1024],
  ]);
});
