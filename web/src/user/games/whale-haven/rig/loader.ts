import type { WhaleRigManifest } from './types';

declare global {
  interface Window {
    __RIG_BASE__?: string;
    __RIG_MANIFEST__?: WhaleRigManifest;
    __EMBEDDED_ASSETS__?: Record<string, string>;
  }
}

export interface LoadedRigBundle {
  manifest: WhaleRigManifest;
  images: Map<string, HTMLImageElement>;
}

let cachedPromise: Promise<LoadedRigBundle> | null = null;
let cacheVersion = 4; // bust stale browser HTTP cache after V4 face calibration

export function getRigBaseUrl(): string {
  if (typeof window !== 'undefined' && window.__RIG_BASE__) {
    return window.__RIG_BASE__;
  }
  return '/assets/whale-haven/rig/whale-maid';
}

function loadImage(url: string): Promise<HTMLImageElement> {
  return new Promise((resolve, reject) => {
    const img = new Image();
    img.decoding = 'async';
    img.onload = () => resolve(img);
    img.onerror = () => reject(new Error(`Failed to load rig layer: ${url}`));
    img.src = url;
  });
}

async function fetchManifest(baseUrl: string, bustCache: boolean): Promise<WhaleRigManifest> {
  if (
    typeof window !== 'undefined' &&
    (window.location.protocol === 'file:' || window.__EMBEDDED_ASSETS__) &&
    window.__RIG_MANIFEST__
  ) {
    return window.__RIG_MANIFEST__;
  }

  try {
    const suffix = bustCache ? `?t=${Date.now()}` : '';
    const res = await fetch(`${baseUrl}/manifest.json${suffix}`);
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    return (await res.json()) as WhaleRigManifest;
  } catch (err) {
    if (typeof window !== 'undefined' && window.__RIG_MANIFEST__) {
      return window.__RIG_MANIFEST__;
    }
    throw err;
  }
}

export function loadWhaleRigBundle(forceReload = false): Promise<LoadedRigBundle> {
  if (forceReload) {
    cachedPromise = null;
    cacheVersion += 1;
  }
  if (cachedPromise) return cachedPromise;

  const baseUrl = getRigBaseUrl();
  const isFileProtocol = typeof window !== 'undefined' && window.location.protocol === 'file:';
  const query = !isFileProtocol && forceReload ? `?v=${cacheVersion}_${Date.now()}` : '';

  cachedPromise = (async () => {
    const manifest = await fetchManifest(baseUrl, forceReload);
    const uniqueFiles = new Set<string>();
    for (const slot of manifest.slots) {
      for (const entry of Object.values(slot.variants)) {
        uniqueFiles.add(entry.file);
      }
    }

    const images = new Map<string, HTMLImageElement>();
    await Promise.all(
      Array.from(uniqueFiles).map(async (relFile) => {
        const embedded =
          typeof window !== 'undefined'
            ? window.__EMBEDDED_ASSETS__?.[`rig/whale-maid/${relFile}`]
            : undefined;
        const img = await loadImage(embedded ?? `${baseUrl}/${relFile}${query}`);
        images.set(relFile, img);
      }),
    );

    return { manifest, images };
  })();

  cachedPromise.catch(() => {
    cachedPromise = null;
  });

  return cachedPromise;
}
