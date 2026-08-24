import type { TFunction } from 'i18next';

/** Keep feature defaults usable until the shared catalogs provide a key. */
export function gameText(
  t: TFunction,
  key: string,
  defaultValue: string,
  options?: Record<string, unknown>,
): string {
  return t(key, { defaultValue, ...(options ?? {}) });
}

export const itemNames: Readonly<Record<string, string>> = Object.freeze({
  whitebait: 'Whitebait',
  gudgeon: 'Gudgeon',
  horse_mouth: 'Horse-mouth fish',
  smelt: 'Smelt',
  loach: 'Loach',
  crucian: 'Crucian carp',
  tilapia: 'Tilapia',
  yellow_catfish: 'Yellow catfish',
  ayu: 'Ayu',
  stream_carp: 'Stream carp',
  common_carp: 'Common carp',
  snakehead: 'Snakehead',
  catfish: 'Catfish',
  mandarin_fish: 'Mandarin fish',
  rainbow_trout: 'Rainbow trout',
  grass_carp: 'Grass carp',
  silver_carp: 'Silver carp',
  bighead_carp: 'Bighead carp',
  black_carp: 'Black carp',
  japanese_eel: 'Japanese eel',
  yellowcheek: 'Yellowcheek',
  taimen: 'Taimen',
  koi: 'Koi',
  boot: 'Old boot',
  seaweed: 'Seaweed',
  plastic_bag: 'Plastic bag',
  branch: 'Branch',
  old_tire: 'Old tire',
  glasses: 'Glasses',
  phone_case: 'Phone case',
  fry: 'Small fry',
  bottle: 'Treasure bottle',
  clover: 'Lucky clover',
  shell: 'Treasure shell',
  unknown: 'Unknown catch',
});

export function itemName(t: TFunction, key: string): string {
  const known = Object.prototype.hasOwnProperty.call(itemNames, key);
  const fallback = known ? itemNames[key] : itemNames.unknown;
  const lookup = known ? `games.fishing.items.${key}` : 'games.fishing.items.unknown';
  return gameText(t, lookup, fallback);
}
