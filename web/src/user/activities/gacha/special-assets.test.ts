import { readFileSync, statSync } from 'node:fs';
import { resolve } from 'node:path';
import { expect, it } from 'vitest';
import { cards, themes } from './catalog';
import { specialCards } from './special-scenes';
import { highResolutionArt } from './art-resolution';
import { momentByKey } from '../../appearance/moments';

it('ships each new SP surface and registered moment layer in both resolutions', () => {
  const composition = JSON.parse(
    readFileSync(resolve('public/assets/gacha/composition.json'), 'utf8'),
  );
  for (const [id, appearance] of Object.entries(specialCards)) {
    const card = cards.find((value) => value.id === id)!;
    const theme = themes.find((value) => value.id === card.theme)!;
    expect(card.appearance).toBe(appearance);
    expect(theme.appearance).toBe(appearance);
    expect(momentByKey.has('cover:' + theme.id)).toBe(true);
    expect(composition.characters[id]).toBe('motion/' + id + '/hero.webp');
    for (const input of composition.cards[id].inputs as string[]) {
      expect(statSync(resolve('public/assets/gacha', input)).size, input).toBeGreaterThan(1000);
    }
    const images = [
      card.art,
      theme.art,
      '/assets/gacha/covers/' + theme.id + '.webp',
      '/assets/gacha/backgrounds/' + theme.id + '.webp',
      '/assets/gacha/motion/' + id + '/hero.webp',
      ...['light', 'dark'].map(
        (mode) => '/assets/gacha/appearances/' + appearance + '-' + mode + '.webp',
      ),
    ];
    const appearanceLayers = composition.cards[id].appearance_layers;
    if (appearanceLayers) {
      for (const mode of ['light', 'dark']) {
        const filename = appearanceLayers.character.replace('{light,dark}', mode);
        const file = resolve('public/assets/gacha', filename);
        expect(statSync(file).size, filename).toBeGreaterThan(1000);
        expect(readFileSync(file).subarray(8, 12).toString(), filename).toBe('WEBP');
        if (appearanceLayers.character_high_resolution) images.push('/assets/gacha/' + filename);
      }
      for (const filename of [appearanceLayers.reflection, appearanceLayers.light].filter(Boolean))
        images.push('/assets/gacha/' + filename);
    }
    for (const src of [...images, ...images.map(highResolutionArt), card.thumb]) {
      const file = resolve('public', '.' + src);
      expect(statSync(file).size, src).toBeGreaterThan(1000);
      expect(readFileSync(file).subarray(8, 12).toString(), src).toBe('WEBP');
    }
  }
});
