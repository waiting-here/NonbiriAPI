import { afterEach, describe, expect, it, vi } from 'vitest';
import { getHistory, startCleanup } from './api';

afterEach(() => vi.unstubAllGlobals());
describe('management history projections', () => {
  it('keeps large snapshot IDs and accepts independent extra response fields', async () => {
    const fetch = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          view: 'groups',
          operations: [],
          groups: [],
          total: '9007199254740993',
          page: 1,
          page_size: 20,
          snapshot_max_id: '9007199254740995',
          additional_summary: {},
        }),
        { status: 200, headers: { 'Content-Type': 'application/json' } },
      ),
    );
    vi.stubGlobal('fetch', fetch);
    const result = await getHistory({ source: 'ai_manual' }, 'groups', '1', 20);
    expect(result.total).toBe('9007199254740993');
    expect(result.snapshot_max_id).toBe('9007199254740995');
    expect(fetch.mock.calls[0]?.[0]).toContain('source=ai_manual');
  });
  it('submits the reviewed cutoff and supplied recovery identity without widening it', async () => {
    const fetch = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          id: '1',
          state: 'pending',
          created_at: 0,
          updated_at: 0,
          actor: { id: '1', name: 'Administrator', role: 'admin' },
          filter: { source: 'ai_auto' },
          cutoff_max_id: '9007199254740993',
          deleted_operations: '0',
          deleted_items: '0',
          skipped_active: '0',
          error_code: '',
        }),
        { status: 202, headers: { 'Content-Type': 'application/json' } },
      ),
    );
    vi.stubGlobal('fetch', fetch);
    const prepared = {
      filter: { source: 'ai_auto' },
      all: false,
      cutoff_max_id: '9007199254740993',
    };
    await startCleanup(prepared, 'same-reviewed-request');
    expect(JSON.parse(fetch.mock.calls[0]?.[1].body as string)).toEqual({
      prepared,
      request_id: 'same-reviewed-request',
    });
  });
});
