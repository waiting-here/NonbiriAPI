import { expect, it } from 'vitest';
import source from '../../../../internal/artwork/catalog.json';
import { createArtworkCatalog, artworkCatalog, type ArtworkCatalog } from './catalog';

it('inherits shared textures without adding scene restrictions to normal card viewing', () => {
  const fixture = structuredClone(source) as ArtworkCatalog;
  fixture.artworks['card:o-claude'].rating = 'r18';
  fixture.artworks['illustration:a'].rating = 'r18';
  const catalog = createArtworkCatalog(fixture);
  expect(catalog.rating('appearance:amber')).toBe('r18');
  expect(catalog.allowed('appearance:amber', true)).toBe(false);
  expect(catalog.allowed('card:a-chatgpt', true)).toBe(true);
  expect(catalog.allowed(catalog.momentKeys('card:a-chatgpt'), true)).toBe(false);
  expect(catalog.allowed('card:o-claude', false)).toBe(true);
  for (const src of [
    '/assets/gacha/cards/o-claude.webp',
    '/assets/gacha/hires/cards/o-claude.webp',
    '/assets/gacha/motion/o-claude/hero.png?v=4',
    '/assets/gacha/motion/o-claude/appearance-hero-light.webp',
    '/assets/gacha/hires/appearances/amber-dark.webp',
  ]) {
    expect(catalog.assetRating(src), src).toBe('r18');
    expect(catalog.sourceAllowed(src, true), src).toBe(false);
  }
});

it('resolves all declared files, nested hires, and neutral shared ornaments', () => {
  for (const rule of source.assets)
    for (const file of rule.files ?? [])
      expect(artworkCatalog.assetRating('/assets/gacha/' + file), file).toBe(
        artworkCatalog.rating(rule.owner),
      );
  expect(
    artworkCatalog.assetRating('/assets/gacha/appearances/layers/hires/celestial-light-hero.webp'),
  ).toBe(artworkCatalog.rating('appearance:celestial'));
  for (const key of ['appearance:default', 'appearance:heart-moment', 'cover:permanent'])
    expect(artworkCatalog.rating(key)).toBe('general');
  for (const src of [
    '/assets/gacha/appearances/layers/cloud.webp',
    '/assets/gacha/backgrounds/covers/permanent.webp',
    '/assets/gacha/layers/covers/permanent.webp',
  ]) {
    expect(artworkCatalog.assetRating(src), src).toBe('general');
  }
  for (const extension of ['webp', 'png', 'jpg', 'jpeg', 'gif', 'avif', 'svg', 'bmp'])
    expect(artworkCatalog.sourceAllowed('/assets/gacha/unknown.' + extension, false)).toBe(false);
  expect(artworkCatalog.sourceAllowed('/assets/gacha/audio/theme.mp3', true)).toBe(true);
  expect(artworkCatalog.sourceAllowed('/assets/other/logo.png', true)).toBe(true);
});

it('uses an exact asset owner before the longest prefix', () => {
  const catalog = createArtworkCatalog({
    version: 1,
    artworks: {
      'card:test': { rating: 'r18' },
      'appearance:test': { rating: 'general' },
      'cover:test': { rating: 'r18' },
    },
    assets: [
      { owner: 'card:test', prefixes: ['motion/test/'] },
      { owner: 'appearance:test', prefixes: ['motion/test/appearance-'] },
      { owner: 'cover:test', files: ['motion/test/appearance-cover.webp'] },
    ],
  });
  expect(catalog.assetRating('/assets/gacha/motion/test/hero.webp')).toBe('r18');
  expect(catalog.assetRating('/assets/gacha/motion/test/appearance-hero.webp')).toBe('general');
  expect(catalog.assetRating('/assets/gacha/motion/test/appearance-cover.webp')).toBe('r18');
});
