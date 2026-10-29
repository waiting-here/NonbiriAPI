import { afterEach, describe, expect, it, vi } from 'vitest';
import { getHistory, getOperationItems, startCleanup } from './api';

const target = {
  ordinal: '1',
  action: 'update',
  target_kind: 'charity_model',
  target_id: '12',
  target_name: 'Saved model',
  outcome: 'succeeded',
  code: '',
};
function itemResponse(items: unknown[]) {
  vi.stubGlobal(
    'fetch',
    vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          total: String(items.length),
          page: 1,
          page_size: 20,
          items,
        }),
        { status: 200, headers: { 'Content-Type': 'application/json' } },
      ),
    ),
  );
}

afterEach(() => vi.unstubAllGlobals());
describe('management history projections', () => {
  it.each([{ details: [] }, { details: 'unavailable' }, { details: null }])(
    'ignores an unusable optional snapshot (%j)',
    async ({ details }) => {
      itemResponse([
        { ...target, details, changes: [{ field: 'enabled', operation: 'updated', after: '1' }] },
      ]);
      const page = await getOperationItems('1', '1', 20);
      expect(page.items[0]?.target_name).toBe('Saved model');
      expect(page.items[0]?.details).toBeUndefined();
      expect(page.items[0]?.changes).toEqual([
        { field: 'enabled', operation: 'updated', after: '1' },
      ]);
    },
  );

  it('drops independent malformed snapshot entries and keeps valid siblings and extensions', async () => {
    itemResponse([
      {
        ...target,
        changes: [
          null,
          {},
          { field: 'broken', operation: 4 },
          { field: 'enabled', operation: 'updated', before: '0', after: '1', extra: true },
        ],
        details: {
          version: 2,
          previous_name: 'Old name',
          state: 'future_state',
          extra: { permitted: true },
          related: [
            null,
            { kind: 'binding', id: '8', name: 42, state: 'deleted' },
            {
              kind: 'binding',
              id: '9',
              name: 'Saved binding',
              state: 'future_state',
              changes: [
                null,
                {},
                {
                  field: 'future_field',
                  operation: 'future_operation',
                  before: {},
                  after: 'new value',
                  unit: 42,
                  extra: true,
                },
              ],
              extra: true,
            },
            { kind: 'binding', id: '10', name: 'Other binding', state: '', changes: false },
          ],
          impacts: [
            null,
            {},
            { kind: 'broken', created: '0', updated: 'invalid', deleted: '0' },
            {
              kind: 'future_kind',
              created: '0',
              updated: '2',
              deleted: '9007199254740993',
              extra: true,
            },
          ],
        },
      },
      { ...target, ordinal: '2', target_name: 'Unaffected sibling' },
    ]);
    const page = await getOperationItems('1', '1', 20);
    expect(page.items).toHaveLength(2);
    expect(page.items[0]?.changes).toEqual([
      { field: 'enabled', operation: 'updated', before: '0', after: '1' },
    ]);
    expect(page.items[0]?.details).toEqual({
      version: 2,
      previous_name: 'Old name',
      state: 'future_state',
      related: [
        {
          kind: 'binding',
          id: '9',
          name: 'Saved binding',
          state: 'future_state',
          changes: [{ field: 'future_field', operation: 'future_operation', after: 'new value' }],
        },
        { kind: 'binding', id: '10', name: 'Other binding', state: '' },
      ],
      impacts: [
        {
          kind: 'future_kind',
          created: '0',
          updated: '2',
          deleted: '9007199254740993',
          sampled: '0',
          omitted: '0',
        },
      ],
    });
    expect(page.items[1]?.target_name).toBe('Unaffected sibling');
  });

  it('ignores malformed optional collections without discarding snapshot metadata', async () => {
    itemResponse([
      {
        ...target,
        changes: {},
        details: {
          version: 1,
          previous_name: 'Old name',
          related: 'unavailable',
          impacts: {},
          state: 4,
        },
      },
    ]);
    const page = await getOperationItems('1', '1', 20);
    expect(page.items[0]?.changes).toEqual([]);
    expect(page.items[0]?.details).toEqual({ version: 1, previous_name: 'Old name' });
  });

  it('still rejects an unusable required target identity', async () => {
    itemResponse([{ ...target, ordinal: null, details: [] }]);
    await expect(getOperationItems('1', '1', 20)).rejects.toMatchObject({
      code: 'invalid_response',
    });
  });

  it('retains safe before/after values, related effects, and missing legacy snapshots', async () => {
    const base = {
      ordinal: '1',
      action: 'delete',
      target_kind: 'charity_model',
      target_id: '12',
      target_name: 'Saved model',
      outcome: 'succeeded',
      code: '',
    };
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        new Response(
          JSON.stringify({
            total: '2',
            page: 1,
            page_size: 20,
            items: [
              {
                ...base,
                changes: [
                  {
                    field: 'request_user_price',
                    operation: 'updated',
                    before: '1000',
                    after: '2000',
                    unit: 'milli_credits',
                  },
                ],
                details: {
                  version: 1,
                  state: 'deleted',
                  previous_name: 'Old name',
                  related: [
                    {
                      kind: 'charity_model_bindings',
                      id: '9',
                      name: 'Saved binding',
                      state: 'deleted',
                      changes: [{ field: 'ord', operation: 'deleted', before: '2' }],
                    },
                  ],
                  impacts: [
                    {
                      kind: 'charity_model_bindings',
                      created: '0',
                      updated: '0',
                      deleted: '9007199254740993',
                    },
                  ],
                },
              },
              { ...base, ordinal: '2' },
            ],
          }),
          { status: 200, headers: { 'Content-Type': 'application/json' } },
        ),
      ),
    );
    const value = await getOperationItems('1', '1', 20);
    expect(value.items[0]?.changes[0]?.after).toBe('2000');
    expect(value.items[0]?.details?.related?.[0]?.changes?.[0]?.before).toBe('2');
    expect(value.items[0]?.details?.impacts?.[0]?.deleted).toBe('9007199254740993');
    expect(value.items[1]?.details).toBeUndefined();
    expect(value.items[1]?.changes).toEqual([]);
  });
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
