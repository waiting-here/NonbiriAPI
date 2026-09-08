import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '@shared/query/http';
import { listKeyBindingsPage } from './browseData';
import { readManualImpacts } from './manualImpacts';
import type { KeyBindingView } from './types';

vi.mock('./browseData', () => ({ listKeyBindingsPage: vi.fn() }));

function page(total: number, current: number, ids?: string[]) {
  const offset = (current - 1) * 100;
  return {
    data: (
      ids ??
      Array.from({ length: Math.min(100, Math.max(0, total - offset)) }, (_, i) =>
        String(offset + i + 1),
      )
    ).map((id) => ({ id, model_id: id, model_full_name: `owner/model-${id}` }) as KeyBindingView),
    next_cursor: null as null,
    pagination: {
      page: String(current),
      page_size: 100 as const,
      total_items: String(total),
      total_pages: String(Math.max(1, Math.ceil(total / 100))),
    },
  };
}

beforeEach(() => vi.resetAllMocks());

describe('exact manual-entry impact preflight', () => {
  it('reads the full 256-binding write set in three bounded pages and retains model identities', async () => {
    vi.mocked(listKeyBindingsPage).mockImplementation(async (_endpoint, _key, window) =>
      page(256, Number(window.page)),
    );
    const controller = new AbortController();
    const result = await readManualImpacts('11', '21', '精确/模型', controller.signal);
    expect(listKeyBindingsPage).toHaveBeenCalledTimes(3);
    expect(listKeyBindingsPage).toHaveBeenNthCalledWith(
      3,
      '11',
      '21',
      { page: '3', pageSize: 100 },
      '精确/模型',
      controller.signal,
    );
    expect(result).toMatchObject({ state: 'complete', count: '256' });
    expect(result.impacts).toHaveLength(256);
    expect(result.impacts[255]).toEqual({
      bindingId: '256',
      modelId: '256',
      modelName: 'owner/model-256',
    });
  });

  it('stops after the first page when the existing replacement limit is exceeded', async () => {
    vi.mocked(listKeyBindingsPage).mockResolvedValue(page(10_017, 1));
    expect(await readManualImpacts('11', '21', 'model')).toEqual({
      state: 'too_many',
      count: '10017',
      impacts: [],
    });
    expect(listKeyBindingsPage).toHaveBeenCalledTimes(1);
  });

  it('accepts an empty exact pair without requesting other models', async () => {
    vi.mocked(listKeyBindingsPage).mockResolvedValue(page(0, 1));
    expect(await readManualImpacts('11', '21', 'absent')).toEqual({
      state: 'complete',
      count: '0',
      impacts: [],
    });
    expect(listKeyBindingsPage).toHaveBeenCalledTimes(1);
  });

  it.each(['changed count', 'overlapping pages'])(
    'refuses a moving snapshot: %s',
    async (reason) => {
      vi.mocked(listKeyBindingsPage)
        .mockResolvedValueOnce(page(101, 1))
        .mockResolvedValueOnce(reason === 'changed count' ? page(102, 2) : page(101, 2, ['1']));
      await expect(readManualImpacts('11', '21', 'model')).rejects.toMatchObject({ status: 409 });
    },
  );

  it.each([new DOMException('Aborted', 'AbortError'), new ApiError('forbidden', 'Denied', 403)])(
    'preserves cancellation or authority loss without partial data: %s',
    async (error) => {
      vi.mocked(listKeyBindingsPage)
        .mockResolvedValueOnce(page(101, 1))
        .mockRejectedValueOnce(error);
      await expect(readManualImpacts('11', '21', 'model')).rejects.toBe(error);
      expect(listKeyBindingsPage).toHaveBeenCalledTimes(2);
    },
  );
});
