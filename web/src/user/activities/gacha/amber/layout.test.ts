import { expect, it } from 'vitest';
import { amberAppearancePlacement, amberLayout } from './layout';

it('keeps the desktop face in the side margin and the soles below the page body', () => {
  const box = amberAppearancePlacement(1672, 1244);
  const face = { x: box.left + box.width / 2, y: box.top + box.height * 0.13 };
  expect(face.x).toBeGreaterThan((1672 + 1280) / 2);
  expect(face.x).toBeLessThan(1672 - 50);
  expect(face.y).toBeGreaterThan(68);
  expect(box.top + box.height * 0.984).toBeLessThan(1244);
});

it('keeps the mobile face below navigation and preserves the character proportions', () => {
  const box = amberAppearancePlacement(390, 844);
  expect(box.left + box.width / 2).toBeGreaterThan(100);
  expect(box.left + box.width / 2).toBeLessThan(340);
  expect(box.top).toBeGreaterThan(68);
  expect(box.width / box.height).toBeCloseTo(1037 / 1516);
});

it('changes the appearance placement independently of the illustration and background crop', () => {
  const illustration = amberLayout(1440, 900, true, true);
  const appearance = amberLayout(1440, 900, true, true, true);
  expect(appearance.view).toEqual(illustration.view);
  expect(appearance.heroBox).not.toEqual(illustration.heroBox);
  expect(illustration.heroBox).toEqual([0.555, 0.045, 0.348, 0.902]);
});
