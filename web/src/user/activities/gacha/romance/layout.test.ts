import { expect, it } from 'vitest';
import { romanceAppearancePlacement, romanceBackdropPlacement } from './layout';
import { heartThiefGroundPoint, heartThiefStance } from './heart-thief-stance';

it.each(['elopement', 'heart-thief'] as const)(
  'keeps the %s face below navigation and inside the desktop margin',
  (theme) => {
    const desktop = romanceAppearancePlacement(1440, 900, theme);
    const mobile = romanceAppearancePlacement(390, 844, theme);
    expect(desktop.left + desktop.width * 0.47).toBeGreaterThan(1280);
    expect(desktop.left + desktop.width * 0.47).toBeLessThan(1410);
    expect(mobile.top).toBeGreaterThan(136);
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

it.each([
  [1440, 900, 920],
  [390, 844, 940],
  [1024, 900, 945],
  [1920, 900, 900],
])(
  'places the supporting heel on the visible marble floor at %s × %s',
  (width, height, minimumFloorY) => {
    const hero = romanceAppearancePlacement(width, height, 'heart-thief');
    const background = romanceBackdropPlacement(width, height, width, 'heart-thief');
    const point = heartThiefGroundPoint(486, 1382);
    const heel = {
      x: hero.left + (point[0] * hero.height) / 1448,
      y: hero.top + (point[1] * hero.height) / 1448,
    };
    expect((heel.y - background.top) / background.scale).toBeGreaterThan(minimumFloorY);
    expect((heel.x - background.left) / background.scale).toBeLessThan(1350);
    expect(background.top).toBeLessThanOrEqual(0);
    expect(background.top + 1024 * background.scale).toBeGreaterThanOrEqual(height);
    const high = romanceBackdropPlacement(width * 2, height * 2, width, 'heart-thief');
    expect(high.left / 2).toBeCloseTo(background.left);
    expect(high.top / 2).toBeCloseTo(background.top);
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
