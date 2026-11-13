import { readFileSync, statSync } from 'node:fs';
import { resolve } from 'node:path';
import { expect, it } from 'vitest';
import { cards, themes } from '../catalog';
import { highResolutionArt } from '../art-resolution';
import { renderLayers } from './layers';
import type { ChristmasCard } from './motion';

it('ships every Christmas portrait, eye state, landscape and registered layer', () => {
  const root = resolve('public/assets/gacha');
  const composition = JSON.parse(readFileSync(resolve(root, 'composition.json'), 'utf8'));
  const theme = themes.find((value) => value.id === 't')!;
  const collection = cards.filter((card) => card.theme === theme.id);
  expect(collection).toHaveLength(5);
  const images = [theme.art, '/assets/gacha/covers/t.webp'];
  const thumbs = [];
  for (const card of collection) {
    const entry = composition.cards[card.id];
    expect(entry.canvas).toEqual([1024, 1536]);
    images.push(
      card.art,
      ...['background', 'hero', 'closed'].map(
        (part) => `/assets/gacha/motion/${card.id}/${part}.webp`,
      ),
    );
    thumbs.push(card.thumb);
    const layers = renderLayers(card.id as ChristmasCard);
    expect(entry.motion_layers.map((layer: { name: string }) => layer.name)).toEqual(
      layers.map((layer) => layer.name),
    );
    for (const layer of layers) {
      const source = `/assets/gacha/motion/${card.id}/parts/${layer.name}.webp`;
      for (const path of [source, highResolutionArt(source)]) {
        const bytes = readFileSync(resolve('public', '.' + path));
        expect(bytes.subarray(8, 12).toString(), path).toBe('WEBP');
        expect(bytes.length).toBeGreaterThan(40);
      }
    }
    images.push(...entry.environment_masks.map((path: string) => '/assets/gacha/' + path));
  }
  for (const kind of ['groups', 'covers']) {
    const entry = composition[kind].t;
    images.push('/assets/gacha/' + entry.background, '/assets/gacha/' + entry.foreground);
  }
  for (const src of [...images, ...images.map(highResolutionArt), ...thumbs]) {
    const path = resolve('public', '.' + src);
    expect(statSync(path).size, src).toBeGreaterThan(1000);
    expect(readFileSync(path).subarray(8, 12).toString(), src).toBe('WEBP');
  }
});
