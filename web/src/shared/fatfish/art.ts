export interface AtlasFrame {
  source: 'walk' | 'expressions';
  rect: [number, number, number, number];
  pivot: [number, number];
  referenceHeight: number;
}
export interface FishAtlas {
  format: 'fat-fish-atlas-v1';
  version: string;
  defaultDisplayHeight: number;
  sources: Record<'walk' | 'expressions', { file: string; width: number; height: number }>;
  animations: Record<string, { frames: string[]; durationsMs: number[]; loop: boolean }>;
  frames: Record<string, AtlasFrame>;
}
export interface FatFishArt {
  atlas: FishAtlas | null;
  sources: Partial<Record<'walk' | 'expressions', HTMLImageElement>>;
  icons: ReadonlyMap<string, HTMLImageElement>;
}

const iconNames = [
  'floor-tile', 'floor-portrait', 'server-wall', 'server-rack', 'router-wedge',
  'keycap-barrier', 'keycap-barrier-vertical', 'cache-puck', 'cooling-fan', 'canteen-monitor', 'rice-sack',
  'memory-module', 'inference-card', 'cooling-fins',
  'rice-goal', 'rice-goal-full', 'switch-off', 'switch-on', 'gate-closed', 'gate-open',
  'rice-arrow', 'offline-pool', 'offline-bubble',
] as const;
const toolIcons: Readonly<Record<string, string>> = {
  barrier: 'keycap-barrier', memory: 'memory-module', fan: 'inference-card',
  light: 'cooling-fins', cup: 'cache-puck',
};
export function toolIcon(resourceKey: string): string | null { return toolIcons[resourceKey] ?? null; }
export const toolNames: Readonly<Record<string, readonly [string, string]>> = {
  barrier: ['空格键帽', 'Space keycap'], memory: ['内存条', 'Memory module'],
  fan: ['推理加速卡', 'Inference card'], light: ['散热鳍片', 'Cooling fins'],
  cup: ['缓存圆墩', 'Cache puck'],
};

function sameOriginAsset(file: string): string {
  if (!/^[a-z0-9][a-z0-9._-]{0,80}\.(?:png|svg)$/.test(file)) throw new Error('Invalid art asset name.');
  return `/assets/fatfish/character/${file}`;
}
function image(src: string): Promise<HTMLImageElement> {
  return new Promise((resolve, reject) => {
    const element = new Image();
    element.onload = () => resolve(element);
    element.onerror = () => reject(new Error('Art asset could not be loaded.'));
    element.src = src;
  });
}
function validAtlas(value: unknown): value is FishAtlas {
  if (!value || typeof value !== 'object') return false;
  const atlas = value as Partial<FishAtlas>;
  if (atlas.format !== 'fat-fish-atlas-v1' || !Number.isFinite(atlas.defaultDisplayHeight) ||
      atlas.defaultDisplayHeight! < 8 || atlas.defaultDisplayHeight! > 128 ||
      atlas.sources?.walk?.file !== 'walk.png' ||
      atlas.sources?.expressions?.file !== 'expressions.png' ||
      typeof atlas.animations !== 'object' || atlas.animations === null ||
      typeof atlas.frames !== 'object' || atlas.frames === null) return false;
  const sources = atlas.sources;
  for (const source of ['walk', 'expressions'] as const) {
    const sheet = sources[source];
    if (!Number.isSafeInteger(sheet.width) || !Number.isSafeInteger(sheet.height) ||
        sheet.width < 1 || sheet.width > 4096 || sheet.height < 1 || sheet.height > 4096) return false;
  }
  const frames = Object.entries(atlas.frames);
  if (frames.length !== 24) return false;
  for (const [id, frame] of frames) {
    if (!/^[a-z-]+-[0-9]{2}$/.test(id) || !frame ||
        frame.source !== 'walk' && frame.source !== 'expressions' ||
        !Array.isArray(frame.rect) || frame.rect.length !== 4 ||
        !frame.rect.every((coordinate) => Number.isSafeInteger(coordinate) && coordinate >= 0) ||
        frame.rect[2] < 1 || frame.rect[3] < 1 ||
        frame.rect[0] + frame.rect[2] > sources[frame.source].width ||
        frame.rect[1] + frame.rect[3] > sources[frame.source].height ||
        !Array.isArray(frame.pivot) || frame.pivot.length !== 2 ||
        !frame.pivot.every((coordinate) => Number.isFinite(coordinate) && Math.abs(coordinate) <= 4096) ||
        !Number.isFinite(frame.referenceHeight) || frame.referenceHeight <= 0 || frame.referenceHeight > 4096)
      return false;
  }
  const names = ['walk-down', 'walk-left', 'walk-right', 'walk-up', 'idle', 'eat'];
  if (Object.keys(atlas.animations).length !== names.length) return false;
  return names.every((name) => {
    const animation = atlas.animations?.[name];
    return animation?.loop === true && animation.frames.length === 4 &&
      animation.durationsMs.length === 4 &&
      animation.frames.every((id) => Object.hasOwn(atlas.frames!, id)) &&
      animation.durationsMs.every((duration) => Number.isSafeInteger(duration) && duration >= 1 && duration <= 5000);
  });
}
let artPromise: Promise<FatFishArt> | null = null;
export function loadFatFishArt(): Promise<FatFishArt> {
  artPromise ??= (async () => {
    const icons = new Map<string, HTMLImageElement>();
    await Promise.all(iconNames.map(async (name) => {
      try { icons.set(name, await image(`/assets/fatfish/svg/${name}.svg`)); }
      catch { /* Geometry remains visible if a visual resource is unavailable. */ }
    }));
    let atlas: FishAtlas | null = null;
    const sources: FatFishArt['sources'] = {};
    try {
      const response = await fetch('/assets/fatfish/character/atlas.json', { cache: 'force-cache' });
      if (response.ok) {
        const data: unknown = await response.json();
        if (validAtlas(data)) {
          atlas = data;
          await Promise.all((['walk', 'expressions'] as const).map(async (source) => {
            try { sources[source] = await image(sameOriginAsset(data.sources[source].file)); }
            catch { /* Fall back to the contour rendering. */ }
          }));
        }
      }
    } catch { /* The game remains usable without visual embellishment. */ }
    return { atlas, sources, icons };
  })();
  return artPromise;
}
