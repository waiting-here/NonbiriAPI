import data from '../../../../internal/artwork/catalog.json';

export type Rating = 'general' | 'r18';
export interface ArtworkDefinition {
  rating: Rating;
  uses?: string[];
  scene?: string;
  name?: string;
  name_en?: string;
}
export interface ArtworkCatalog {
  version: number;
  artworks: Record<string, ArtworkDefinition>;
  assets: { owner: string; files?: string[]; prefixes?: string[] }[];
}
export function createArtworkCatalog(input: ArtworkCatalog) {
  const ratings = new Map<string, Rating>();
  const rating = (key: string, visiting = new Set<string>()): Rating | undefined => {
    if (ratings.has(key)) return ratings.get(key);
    const item = input.artworks[key];
    if (!item) return undefined;
    if (visiting.has(key)) throw new Error('Artwork dependency cycle');
    visiting.add(key);
    let result = item.rating;
    for (const dependency of item.uses ?? []) {
      const inherited = rating(dependency, visiting);
      if (!inherited) throw new Error('Unknown artwork dependency');
      if (inherited === 'r18') result = 'r18';
    }
    visiting.delete(key);
    ratings.set(key, result);
    return result;
  };
  for (const key of Object.keys(input.artworks)) rating(key);
  const files = new Map<string, string>();
  const prefixes: [string, string][] = [];
  for (const rule of input.assets) {
    for (const path of rule.files ?? []) files.set(path, rule.owner);
    for (const prefix of rule.prefixes ?? []) prefixes.push([prefix, rule.owner]);
  }
  prefixes.sort((a, b) => b[0].length - a[0].length);
  const assetRating = (src: string) => {
    const path = src.split(/[?#]/, 1)[0];
    if (!path.startsWith('/assets/gacha/')) return undefined;
    const relative = path.slice('/assets/gacha/'.length);
    const owner =
      files.get(relative) ?? prefixes.find(([prefix]) => relative.startsWith(prefix))?.[1];
    return owner ? rating(owner) : undefined;
  };
  return {
    rating,
    momentKeys: (key: string) =>
      input.artworks[key]?.scene ? [key, input.artworks[key].scene!] : [key],
    assetRating,
    allowed: (keys: string | readonly string[], disabled: boolean) =>
      (typeof keys === 'string' ? [keys] : keys).every((key) => {
        const value = rating(key);
        return value === 'general' || (value === 'r18' && !disabled);
      }),
    sourceAllowed: (src: string, disabled: boolean) => {
      const value = assetRating(src);
      if (value) return value === 'general' || !disabled;
      return !/^\/assets\/gacha\/.*\.(?:webp|png|jpe?g|gif|avif|svg|bmp)(?:[?#]|$)/i.test(src);
    },
  };
}
export const artworkCatalog = createArtworkCatalog(data as ArtworkCatalog);
export const artworkCovers = Object.entries(data.artworks)
  .filter(([key]) => key.startsWith('cover:'))
  .map(([key, value]) => ({
    id: key.slice('cover:'.length),
    name: (value as ArtworkDefinition).name!,
    name_en: (value as ArtworkDefinition).name_en!,
  }));
