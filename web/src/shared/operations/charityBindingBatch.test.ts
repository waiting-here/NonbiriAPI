import { describe, expect, it, vi } from 'vitest';
import {
  addKeyBindings,
  normalizeBindingPreset,
  normalizeBindingReceipt,
  saveBindingPreset,
} from './charityBindingBatch';

const channel = `mch_${'A'.repeat(22)}`;
const preset = { channel_id: channel, channel_revision: '1', preset_revision: '0', mappings: [] };
const receipt = {
  items: [{ donation_key_id: '21', results: ['added', 'missing_model', 'already_bound'] }],
  models: [{ charity_model_id: '31', binding_revision: '2' }],
  added_count: '1',
};
describe('charity batch wire contract', () => {
  it('accepts the existing 256-item manual capacity and rejects larger result matrices', () => {
    const full = {
      items: [{ donation_key_id: '21', results: Array.from({ length: 256 }, () => 'added') }],
      models: Array.from({ length: 256 }, (_, index) => ({
        charity_model_id: String(index + 1),
        binding_revision: '1',
      })),
      added_count: '256',
    };
    expect(normalizeBindingReceipt(full, 256, '21').items[0]?.results).toHaveLength(256);
    expect(normalizeBindingReceipt(full, 256, '21').models).toHaveLength(256);
    expect(() =>
      normalizeBindingReceipt(
        {
          ...full,
          items: [{ donation_key_id: '21', results: [...full.items[0]!.results, 'added'] }],
          added_count: '257',
        },
        257,
        '21',
      ),
    ).toThrow();
    expect(() =>
      normalizeBindingReceipt(
        { ...full, models: [...full.models, { charity_model_id: '257', binding_revision: '1' }] },
        256,
        '21',
      ),
    ).toThrow();
  });
  it('keeps missing targets visible and checks compact result dimensions and counts', () => {
    expect(
      normalizeBindingPreset({
        ...preset,
        mappings: [
          {
            upstream_model_id: 'exact/upstream',
            charity_model_id: '31',
            full_name: null,
            binding_revision: null,
          },
        ],
      }).mappings[0]?.full_name,
    ).toBeNull();
    expect(normalizeBindingReceipt(receipt, 3, '21').items[0]?.results).toEqual([
      'added',
      'missing_model',
      'already_bound',
    ]);
    expect(() => normalizeBindingReceipt(receipt, 2, '21')).toThrow();
    expect(() => normalizeBindingReceipt(receipt, 3, '22')).toThrow();
    expect(() => normalizeBindingReceipt({ ...receipt, added_count: '2' }, 3)).toThrow();
  });
  it('sends exact revisions and the supplied retry identity without labels', async () => {
    const requests: { path: string; body: unknown; key: string | null }[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn<typeof fetch>(async (input, init) => {
        requests.push({
          path: String(input),
          body: JSON.parse(String(init?.body)),
          key: new Headers(init?.headers).get('Idempotency-Key'),
        });
        return new Response(
          JSON.stringify(
            init?.method === 'PUT'
              ? preset
              : { ...receipt, items: [{ donation_key_id: '21', results: ['added'] }] },
          ),
          { headers: { 'content-type': 'application/json' } },
        );
      }),
    );
    const body = {
      expected_donation_revision: '4',
      targets: [
        {
          charity_model_id: '31',
          expected_binding_revision: '1',
          upstream_model_id: 'Exact/Upstream',
        },
      ],
    };
    await addKeyBindings('steward', '7', '21', body, 'same-operation');
    await saveBindingPreset(
      'admin',
      channel,
      { expected_channel_revision: '1', expected_preset_revision: '0', mappings: [] },
      'same-save',
    );
    expect(requests[0]).toEqual({
      path: '/api/steward/donations/7/keys/21/bindings/batch',
      body,
      key: 'same-operation',
    });
    expect(requests[1]?.path).toBe(`/admin/api/charity-binding-presets/${channel}`);
    expect(requests[1]?.key).toBe('same-save');
  });
});
