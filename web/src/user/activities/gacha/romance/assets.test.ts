import { expect, it } from 'vitest';
import { romanceAssetSources } from './assets';

it('loads the selected night painting at high resolution while preserving the registered character plates', () => {
  const standard = romanceAssetSources('elopement');
  const high = romanceAssetSources('elopement', true, true);
  expect(standard.background).toBe('/assets/gacha/motion/p-claude/background-light.webp');
  expect(high.background).toBe('/assets/gacha/hires/motion/p-claude/background-dark.webp');
  for (const name of ['offer', 'arrival', 'closed', 'clean']) {
    expect(high[name]).toBe(standard[name]);
    expect(high[name]).toMatch(/\.webp$/);
  }
});

it('loads both heart-thief lighting variants without resampling the character registration', () => {
  const standard = romanceAssetSources('heart-thief');
  const high = romanceAssetSources('heart-thief', true, true);
  expect(standard.background).toBe('/assets/gacha/motion/q-glm/background-light.webp');
  expect(high.background).toBe('/assets/gacha/hires/motion/q-glm/background-dark.webp');
  for (const name of ['character', 'hair-backing-v3']) {
    expect(high[name]).toBe(standard[name]);
    expect(high[name]).toMatch(/\.webp$/);
  }
});
