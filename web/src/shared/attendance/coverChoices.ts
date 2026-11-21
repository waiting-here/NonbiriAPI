import { artworkCatalog, artworkCovers } from '@shared/artwork/catalog';
import { themes } from '../../../../internal/gacha/catalog.json';
import { isPublicLogoURL } from '@shared/query/publicConfig';
import type { AttendanceCover } from './api';

const collectionArt = themes.map(({ id, name, name_en, art }) => ({ id, name, name_en, src: art }));
const poolArt = artworkCovers.map((cover) => ({
  ...cover,
  src: '/assets/gacha/covers/' + cover.id + '.webp',
}));
const catalogs = { pool: poolArt, collection: collectionArt };
const prefixes = { pool: 'cover:', collection: 'illustration:' };

export const defaultCover = '/assets/gacha/groups/a.webp';

export function campaignCoverChoices(kind: keyof typeof catalogs, english: boolean) {
  return catalogs[kind].map((item) => {
    const key = prefixes[kind] + item.id;
    const name = english ? item.name_en : item.name;
    return {
      value: item.id,
      artworkKey: key,
      label: artworkCatalog.rating(key) === 'r18' ? name + ' · R-18' : name,
    };
  });
}

export function resolveCover(cover?: AttendanceCover) {
  if (!cover) return undefined;
  if (cover.kind === 'external') {
    return isPublicLogoURL(cover.value) ? { src: cover.value } : undefined;
  }
  const item = catalogs[cover.kind]?.find(({ id }) => id === cover.value);
  return item ? { src: item.src, artworkKey: prefixes[cover.kind] + item.id } : undefined;
}
