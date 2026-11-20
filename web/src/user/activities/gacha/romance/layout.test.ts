import { expect, it } from 'vitest';
import {
  heartThiefLandscapePlacement,
  romanceAppearancePlacement,
  romanceBackdropPlacement,
} from './layout';
import { heartThiefGroundPoint, heartThiefStance } from './heart-thief-stance';

it.each(['elopement', 'heart-thief'] as const)(
  'keeps the %s face below navigation and inside the desktop margin',
  (theme) => {
    const desktop = romanceAppearancePlacement(1440, 900, theme);
    const mobile = romanceAppearancePlacement(390, 844, theme);
    expect(desktop.left + desktop.width * 0.47).toBeGreaterThan(1280);
    expect(desktop.left + desktop.width * 0.47).toBeLessThan(1410);
    expect(mobile.top + mobile.height * 0.13).toBeGreaterThan(136);
    expect(mobile.top + mobile.height).toBeLessThan(844);
    expect(mobile.left + mobile.width * 0.47).toBeGreaterThan(100);
    expect(mobile.left + mobile.width * 0.47).toBeLessThan(340);
    expect(desktop.width / desktop.height).toBeCloseTo(mobile.width / mobile.height);
  },
);

it('preserves mobile background framing at high device pixel ratios', () => {
  const standard = romanceBackdropPlacement(390, 844, 390);
  const high = romanceBackdropPlacement(1170, 2532, 390);
  expect(high.left / 3).toBeCloseTo(standard.left);
  expect(high.scale / 3).toBeCloseTo(standard.scale);
});

const rearFloorEdge = (x: number) => 760 + ((x - 430) * 165) / 970;

it.each([
  [1440, 900],
  [390, 844],
  [1024, 900],
  [1920, 900],
  [1440, 831],
  [1920, 532],
  [2560, 720],
])(
  'keeps the supporting shoe in frame and the figure at room scale where space permits at %s × %s',
  (width, height) => {
    const hero = romanceAppearancePlacement(width, height, 'heart-thief');
    const background = romanceBackdropPlacement(width, height, width, 'heart-thief');
    if (width >= 1024) {
      const supportedHeight =
        (hero.height * heartThiefStance.anchor[1]) / heartThiefStance.contentHeight;
      expect(hero.top + supportedHeight).toBeLessThanOrEqual(height * 0.995 + 0.001);
      // Keep the figure at least twice the room console's height whenever it fits.
      // If the wide room makes that impossible, keep the largest fully grounded figure.
      const maximum = (height * 0.92 * heartThiefStance.contentHeight) / heartThiefStance.anchor[1];
      expect(hero.height).toBeGreaterThanOrEqual(
        Math.min(2 * 404 * background.scale, maximum) - 0.001,
      );
    }
    for (const [x, y] of [heartThiefStance.anchor, [486, 1382]]) {
      const point = heartThiefGroundPoint(x, y);
      const footX =
        (hero.left + (point[0] * hero.height) / 1448 - background.left) / background.scale;
      const footY =
        (hero.top + (point[1] * hero.height) / 1448 - background.top) / background.scale;
      expect(footY - rearFloorEdge(footX)).toBeGreaterThan(20);
    }
    expect(background.top).toBeLessThanOrEqual(0);
    expect(background.top + 1024 * background.scale).toBeGreaterThanOrEqual(height);
    const high = romanceBackdropPlacement(width * 2, height * 2, width, 'heart-thief');
    expect(high.left / 2).toBeCloseTo(background.left);
    expect(high.top / 2).toBeCloseTo(background.top);
  },
);

it.each([false, true])(
  'keeps both supporting contacts on the floor in the landscape camera (cover=%s)',
  (cover) => {
    const hero = heartThiefLandscapePlacement(cover);
    expect(hero.top).toBeGreaterThan(0);
    for (const [x, y] of [heartThiefStance.anchor, [486, 1382]]) {
      const point = heartThiefGroundPoint(x, y);
      const footX = hero.left + point[0] * hero.scale;
      const footY = hero.top + point[1] * hero.scale;
      expect(footY - hero.backgroundTop - rearFloorEdge(footX)).toBeGreaterThan(40);
      expect(footY).toBeLessThan(844);
    }
  },
);

it('anchors the supporting forefoot while preserving the rigid toe-to-heel distance', () => {
  const toe = heartThiefGroundPoint(...heartThiefStance.anchor);
  const heel = heartThiefGroundPoint(486, 1382);
  expect(toe).toEqual(heartThiefStance.anchor);
  expect(Math.hypot(heel[0] - toe[0], heel[1] - toe[1])).toBeCloseTo(Math.hypot(65, -55));
  expect(heel[0]).toBeLessThan(486);
  expect(heartThiefStance.plateHeight).toBeGreaterThan(heartThiefStance.contentHeight);
});
