export const fishFamilies = [
  'minnow',
  'eel',
  'panfish',
  'catfish',
  'carp',
  'predator',
  'salmonid',
] as const;

export type FishFamily = (typeof fishFamilies)[number];
export type FishPalette =
  | 'pearl'
  | 'sand'
  | 'amber'
  | 'ice'
  | 'umber'
  | 'bronze'
  | 'slate'
  | 'gold'
  | 'jade'
  | 'olive'
  | 'moss'
  | 'coral'
  | 'ink';
export type FishPattern =
  'plain' | 'stripe' | 'spot' | 'band' | 'belly' | 'scale' | 'saddle' | 'patch';
export type FinStyle = 'soft' | 'round' | 'forked' | 'swept' | 'spined';

export interface FishArtDescriptor {
  readonly kind: 'fish';
  readonly key: string;
  readonly messageKey: string;
  readonly family: FishFamily;
  readonly palette: FishPalette;
  readonly pattern: FishPattern;
  readonly fin: FinStyle;
  readonly barbels: boolean;
}

export interface ObjectArtDescriptor {
  readonly kind: 'junk' | 'treasure';
  readonly key: string;
  readonly messageKey: string;
}

export interface UnknownArtDescriptor {
  readonly kind: 'unknown';
  readonly key: 'unknown';
  readonly messageKey: 'games.fishing.items.unknown';
}

export type FishingArtDescriptor = FishArtDescriptor | ObjectArtDescriptor | UnknownArtDescriptor;

const fish = (
  key: string,
  family: FishFamily,
  palette: FishPalette,
  pattern: FishPattern,
  fin: FinStyle,
  barbels = false,
): FishArtDescriptor =>
  Object.freeze({
    kind: 'fish',
    key,
    messageKey: `games.fishing.items.${key}`,
    family,
    palette,
    pattern,
    fin,
    barbels,
  });

const objectArt = (key: string, kind: ObjectArtDescriptor['kind']): ObjectArtDescriptor =>
  Object.freeze({ kind, key, messageKey: `games.fishing.items.${key}` });

export const fishArtwork = Object.freeze([
  fish('whitebait', 'minnow', 'pearl', 'plain', 'forked'),
  fish('gudgeon', 'minnow', 'sand', 'stripe', 'round'),
  fish('horse_mouth', 'minnow', 'amber', 'belly', 'swept'),
  fish('smelt', 'minnow', 'ice', 'band', 'forked'),
  fish('loach', 'eel', 'umber', 'spot', 'soft'),
  fish('crucian', 'panfish', 'bronze', 'scale', 'round'),
  fish('tilapia', 'panfish', 'slate', 'stripe', 'spined'),
  fish('yellow_catfish', 'catfish', 'gold', 'plain', 'soft', true),
  fish('ayu', 'panfish', 'jade', 'band', 'forked'),
  fish('stream_carp', 'panfish', 'olive', 'spot', 'swept'),
  fish('common_carp', 'carp', 'bronze', 'scale', 'swept', true),
  fish('snakehead', 'predator', 'moss', 'saddle', 'spined'),
  fish('catfish', 'catfish', 'slate', 'patch', 'round', true),
  fish('mandarin_fish', 'predator', 'amber', 'spot', 'spined'),
  fish('rainbow_trout', 'salmonid', 'coral', 'stripe', 'forked'),
  fish('grass_carp', 'carp', 'olive', 'plain', 'swept'),
  fish('silver_carp', 'carp', 'ice', 'scale', 'round'),
  fish('bighead_carp', 'carp', 'slate', 'patch', 'round'),
  fish('black_carp', 'carp', 'ink', 'plain', 'swept'),
  fish('japanese_eel', 'eel', 'ink', 'band', 'soft'),
  fish('yellowcheek', 'predator', 'gold', 'belly', 'forked'),
  fish('taimen', 'salmonid', 'umber', 'spot', 'swept'),
  fish('koi', 'carp', 'pearl', 'patch', 'swept', true),
] satisfies readonly FishArtDescriptor[]);

export const junkArtwork = Object.freeze([
  objectArt('boot', 'junk'),
  objectArt('seaweed', 'junk'),
  objectArt('plastic_bag', 'junk'),
  objectArt('branch', 'junk'),
  objectArt('old_tire', 'junk'),
  objectArt('glasses', 'junk'),
  objectArt('phone_case', 'junk'),
  objectArt('fry', 'junk'),
] satisfies readonly ObjectArtDescriptor[]);

export const treasureArtwork = Object.freeze([
  objectArt('bottle', 'treasure'),
  objectArt('clover', 'treasure'),
  objectArt('shell', 'treasure'),
] satisfies readonly ObjectArtDescriptor[]);

export const unknownArtwork: UnknownArtDescriptor = Object.freeze({
  kind: 'unknown',
  key: 'unknown',
  messageKey: 'games.fishing.items.unknown',
});

export const fishingArtwork = Object.freeze([
  ...fishArtwork,
  ...junkArtwork,
  ...treasureArtwork,
] satisfies readonly FishingArtDescriptor[]);

const registry = new Map<string, FishingArtDescriptor>(
  fishingArtwork.map((descriptor) => [descriptor.key, descriptor]),
);

export function resolveFishingArtwork(key: string): FishingArtDescriptor {
  return registry.get(key) ?? unknownArtwork;
}
