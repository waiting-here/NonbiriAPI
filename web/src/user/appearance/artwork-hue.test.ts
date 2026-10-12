import { expect, it } from 'vitest';
import { artworkHue } from './artwork-hue';

it('uses a calm fallback for neutral or fully transparent artwork', () => {
  expect(artworkHue(new Uint8ClampedArray([0, 0, 0, 255, 255, 255, 255, 255]))).toBe(215);
  expect(artworkHue(new Uint8ClampedArray([255, 0, 0, 0]))).toBe(215);
});

it('keeps warm and cool artwork distinct and handles the red hue boundary', () => {
  expect(artworkHue(new Uint8ClampedArray([255, 128, 0, 255]))).toBe(30);
  expect(artworkHue(new Uint8ClampedArray([0, 128, 255, 255]))).toBe(210);
  const red = artworkHue(new Uint8ClampedArray([255, 0, 8, 255, 255, 8, 0, 255]));
  expect(Math.min(red, 360 - red)).toBeLessThan(2);
});
