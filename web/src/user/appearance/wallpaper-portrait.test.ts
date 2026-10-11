import { expect, it } from 'vitest';
import { portraitFlex, portraitRigs } from './wallpaper-portrait';

it('pins the face, shoulders, hands, torso and seated legs in every portrait', () => {
  const landmarks = {
    'wandering-light': [
      [0.76, 0.22],
      [0.74, 0.33],
      [0.72, 0.57],
      [0.79, 0.43],
      [0.72, 0.73],
    ],
    'wandering-dark': [
      [0.68, 0.22],
      [0.7, 0.35],
      [0.7, 0.58],
      [0.78, 0.49],
      [0.87, 0.66],
    ],
    'celestial-light': [
      [0.78, 0.17],
      [0.79, 0.32],
      [0.78, 0.57],
      [0.73, 0.49],
      [0.61, 0.67],
      [0.5, 0.93],
    ],
    'celestial-dark': [
      [0.77, 0.17],
      [0.79, 0.34],
      [0.74, 0.6],
      [0.74, 0.46],
      [0.57, 0.67],
      [0.38, 0.94],
    ],
  } as const;
  for (const id of Object.keys(landmarks) as (keyof typeof landmarks)[])
    for (const point of landmarks[id])
      expect(portraitFlex(id, point), id + ' ' + point).toEqual([0, 0]);
});

it('has bounded, softly pinned motion only in the authored free regions', () => {
  for (const id of Object.keys(portraitRigs) as (keyof typeof portraitRigs)[]) {
    const maxima = [0, 0];
    for (let y = 0; y <= 100; y++)
      for (let x = 0; x <= 100; x++) {
        const values = portraitFlex(id, [x / 100, y / 100]);
        values.forEach((value, i) => {
          expect(value).toBeGreaterThanOrEqual(0);
          expect(value).toBeLessThanOrEqual(1);
          maxima[i] = Math.max(maxima[i], value);
        });
      }
    expect(maxima[0]).toBeGreaterThan(0.6);
    expect(maxima[1]).toBeGreaterThan(0.6);
    for (const region of [...portraitRigs[id].hair, ...portraitRigs[id].cloth])
      expect(portraitFlex(id, region.root)).toEqual([0, 0]);
  }
});
