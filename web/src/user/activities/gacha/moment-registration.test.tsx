import { renderHook, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { useMomentExpansion } from './moment-registration';

afterEach(() => vi.unstubAllGlobals());

it('loads one public manifest and keeps unregistered cards on their original frame', async () => {
  const expansion = {
    mode: 'native-camera',
    canvas: [1536, 1536],
    originalRegion: { x: 256, y: 0, width: 1024, height: 1536 },
  };
  const fetcher = vi.fn(
    async () =>
      new Response(
        JSON.stringify({
          version: 1,
          cards: {
            'q-glm': { moment_layers: { expansion } },
            'a-chatgpt': { canvas: [1024, 1536] },
            's-claude': {
              moment_layers: {
                scene: {
                  path: 'moment/s-claude/background.webp',
                  canvas: [2731, 1536],
                  originalRegion: { x: 1707, y: 0, width: 1024, height: 1536 },
                },
              },
            },
          },
        }),
        { headers: { 'content-type': 'application/json' } },
      ),
  );
  vi.stubGlobal('fetch', fetcher);
  const view = renderHook(({ id }) => useMomentExpansion(id), { initialProps: { id: 'q-glm' } });
  await waitFor(() => expect(view.result.current.ready).toBe(true));
  expect(view.result.current.expansion).toEqual(expansion);
  view.rerender({ id: 'a-chatgpt' });
  expect(view.result.current.expansion).toBeUndefined();
  view.rerender({ id: 'q-glm' });
  await waitFor(() => expect(view.result.current.expansion).toEqual(expansion));
  view.rerender({ id: 's-claude' });
  expect(view.result.current.scene?.path).toBe('moment/s-claude/background.webp');
  expect(view.result.current.expansion).toBeUndefined();
  expect(fetcher).toHaveBeenCalledTimes(1);
  expect(fetcher).toHaveBeenCalledWith('/assets/gacha/composition.json');
});
