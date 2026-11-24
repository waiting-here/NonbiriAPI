import type {
  CodexPetManifest,
  CodexPetStateId,
  LoadedCodexPetAssets,
  LoadedPetSheet,
  PetHighFpsLayout,
  PetHighFpsStateSlice,
} from './types';
import { getSceneAssetUrl } from '../game/catalog';
const OUTFIT_TO_PET_ID: Record<string, string> = {
  maid_classic: 'whale-maid',
  winter_snow: 'whale-winter',
  knit_beret: 'whale-knit',
  whale_hoodie: 'whale-hoodie',
};

export function resolveOutfitPetId(outfit?: string, isWorkerUniform?: boolean): string {
  if (isWorkerUniform) return 'whale-maid';
  if (outfit && outfit in OUTFIT_TO_PET_ID) {
    return OUTFIT_TO_PET_ID[outfit];
  }
  return 'whale-maid';
}

type Motion = CodexPetStateId | 'feast' | 'workerFeast' | 'workerIdle' | 'sleeping' | 'look360';
const ready = Promise.resolve();
const cache = new Map<string, Promise<LoadedCodexPetAssets>>();
const pending = new WeakMap<LoadedCodexPetAssets, Map<Motion, Promise<void>>>();
export function invalidateCodexPetCache(petId: string) {
  cache.delete(petId);
}
async function manifest<T>(url: string): Promise<T> {
  const res = await fetch(url);
  if (!res.ok) throw new Error('角色动作暂时无法加载。');
  return res.json();
}
async function sheet(
  base: string,
  slice: PetHighFpsStateSlice,
  layout: PetHighFpsLayout,
): Promise<LoadedPetSheet> {
  const image = new Image();
  image.src = base + '/' + slice.sheet;
  await image.decode();
  return {
    image,
    columns: slice.columns ?? layout.columns,
    cellWidth: slice.cellWidth ?? layout.cellWidth,
    cellHeight: slice.cellHeight ?? layout.cellHeight,
  };
}
export function requestMotion(assets: LoadedCodexPetAssets, motion: Motion): Promise<void> {
  const requests = pending.get(assets)!;
  const existing = requests.get(motion);
  if (existing) return existing;
  const layout = assets.highFpsLayout!;
  const extra =
    motion === 'workerIdle' ||
    motion === 'workerFeast' ||
    motion === 'sleeping' ||
    motion === 'feast' ||
    motion === 'look360';
  const slice = extra ? layout[motion] : layout.states[motion];
  if (!slice?.sheet || (!extra && assets.stateSheets?.[motion])) return ready;
  const task = sheet(getSceneAssetUrl('pet/' + assets.manifest.id), slice, layout)
    .then((loaded) => {
      if (extra) assets[(motion + 'Sheet') as 'workerIdleSheet'] = loaded;
      else assets.stateSheets![motion] = loaded;
    })
    .catch(() => {
      /* Keep the original idle frame if a motion is unavailable. */
    });
  requests.set(motion, task);
  return task;
}
export function loadCodexPetAssets(id: string): Promise<LoadedCodexPetAssets> {
  const existing = cache.get(id);
  if (existing) return existing;
  const base = getSceneAssetUrl('pet/' + id);
  const task = Promise.all([
    manifest<CodexPetManifest>(base + '/pet.json'),
    manifest<PetHighFpsLayout>(base + '/pet-i2v.json'),
  ]).then(async ([manifest, layout]) => {
    const idle = await sheet(base, layout.states.idle!, layout);
    const assets: LoadedCodexPetAssets = {
      manifest,
      atlasImage: idle.image,
      cellWidth: idle.cellWidth,
      cellHeight: idle.cellHeight,
      stateSheets: { idle },
      highFpsLayout: layout,
      hasLookDirections: Boolean(layout.look360),
      isHdAtlas: true,
    };
    pending.set(assets, new Map());
    return assets;
  });
  cache.set(id, task);
  task.catch(() => cache.delete(id));
  return task;
}
