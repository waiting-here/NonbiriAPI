import { afterEach, expect, it, vi } from 'vitest';
import { invalidateCodexPetCache, loadCodexPetAssets, requestMotion } from './loader';

afterEach(() => {
  invalidateCodexPetCache('whale-maid');
  vi.unstubAllGlobals();
});

it('retries a failed gesture on the next interaction and shares its in-flight decode', async () => {
  const layout = {
    columns: 1,
    cellWidth: 2,
    cellHeight: 2,
    states: { idle: { sheet: 'idle.webp' }, waving: { sheet: 'wave.webp' } },
  };
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) =>
      Response.json(url.endsWith('/pet.json') ? { id: 'whale-maid' } : layout),
    ),
  );
  let waveAttempts = 0;
  vi.stubGlobal(
    'Image',
    class {
      src = '';
      async decode() {
        if (this.src.endsWith('/wave.webp') && ++waveAttempts === 1)
          throw new Error('decode failed');
      }
    },
  );

  const assets = await loadCodexPetAssets('whale-maid');
  expect(await requestMotion(assets, 'waving', true)).toBe(false);
  expect(await requestMotion(assets, 'waving')).toBe(false);
  expect(waveAttempts).toBe(1);
  const retry = requestMotion(assets, 'waving', true);
  expect(requestMotion(assets, 'waving', true)).toBe(retry);
  expect(await retry).toBe(true);
  expect(await requestMotion(assets, 'waving')).toBe(true);
  expect(waveAttempts).toBe(2);
  expect(assets.stateSheets?.waving?.image.src).toMatch(/wave\.webp$/);
});
