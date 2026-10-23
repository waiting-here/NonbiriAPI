import { readdirSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { expect, it } from 'vitest';
import source from '../../../internal/artwork/catalog.json';
import { artworkCatalog } from '../../src/shared/artwork/catalog';

function imageFiles(root: string, prefix = ''): string[] {
  return readdirSync(join(root, prefix), { withFileTypes: true }).flatMap((entry) => {
    const relative = prefix + entry.name;
    if (entry.isDirectory()) return imageFiles(root, relative + '/');
    return entry.isFile() && /\.(webp|png|jpe?g|avif|gif|svg|bmp)$/i.test(entry.name)
      ? [relative]
      : [];
  });
}

it('classifies every bundled artwork image and rejects stale asset rules', () => {
  const files = new Set(
    imageFiles(fileURLToPath(new URL('../../public/assets/gacha', import.meta.url))),
  );
  expect(files.size).toBeGreaterThan(0);
  for (const file of files)
    expect(artworkCatalog.assetRating('/assets/gacha/' + file), file).toBeDefined();
  for (const rule of source.assets) {
    for (const file of rule.files ?? []) expect(files.has(file), file).toBe(true);
    for (const prefix of rule.prefixes ?? [])
      expect(
        [...files].some((file) => file.startsWith(prefix)),
        prefix,
      ).toBe(true);
  }
});
