import { afterEach, expect, it, vi } from 'vitest';
import { renderSSR } from '../../user/activities/gacha/ssr-renderer';
import { renderSpecial } from '../../user/activities/gacha/special-renderer';
import { renderWallpaper } from '../../user/appearance/wallpaper-motion';

afterEach(() => vi.unstubAllGlobals());
it('does not start image loads for a scene retired before a lazy renderer resolves', async () => {
  vi.stubGlobal('Path2D', class {});
  const { renderRomance } = await import('../../user/activities/gacha/romance/renderer');
  const requests: string[] = [];
  vi.stubGlobal(
    'Image',
    class {
      set src(value: string) {
        requests.push(value);
      }
    },
  );
  const controller = new AbortController();
  controller.abort();
  const canvas = document.createElement('canvas');
  await renderSSR(canvas, 'g-chatgpt', undefined, undefined, false, { signal: controller.signal });
  await renderSSR(canvas, 'q-glm', undefined, undefined, false, { signal: controller.signal });
  await renderSpecial(canvas, 'heart-thief', { signal: controller.signal });
  await renderRomance(canvas, 'heart-thief', { signal: controller.signal });
  await renderWallpaper(canvas, 'celestial', 'light', controller.signal, 'standard');
  expect(requests).toEqual([]);
});
