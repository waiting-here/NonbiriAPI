import { expect, it } from 'vitest';
import { mirrorSeaAppearancePlacement, mirrorSeaLayout } from './mirror-sea-layout';
import { mirrorSea } from './mirror-sea-config';

it.each([
  [1440, 900],
  [390, 844],
  [1024, 900],
  [2560, 1080],
  [5120, 1440],
  [844, 390],
])('keeps the raised fingertips inside the original mirror at %d × %d', (width, height) => {
  const box = mirrorSeaAppearancePlacement(width, height);
  const [x, y, w, h] = box.heroBox;
  const hand = [x + w * 0.145, y + h * 0.128];
  expect(Math.hypot((hand[0] - 0.704) / 0.108, (hand[1] - 0.43) / 0.353)).toBeLessThanOrEqual(
    0.900001,
  );
  expect(box.top).toBeGreaterThan(68);
  expect(box.top + box.height).toBeLessThan(height);
  expect(y + h * 0.987).toBeGreaterThan(0.96);
  const faceX = box.left + box.width * 0.46;
  expect(faceX).toBeGreaterThan(0);
  expect(faceX).toBeLessThan(width);
  expect(box.backgroundPosition).toBeGreaterThanOrEqual(0);
  expect(box.backgroundPosition).toBeLessThanOrEqual(1);
});

it('preserves the registered portrait and landscape placement outside the appearance view', () => {
  expect(mirrorSeaLayout(1672, 941, true, true).heroBox).toEqual(mirrorSea.landscapeHero);
  expect(mirrorSeaLayout(1024, 1536, false, false).view).toEqual(mirrorSea.portraitCrop);
});
