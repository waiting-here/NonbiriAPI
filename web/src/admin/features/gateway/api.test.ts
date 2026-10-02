import { describe, expect, it, vi } from 'vitest';
import { gatewayAdapterEfforts } from '@shared/gateway/capabilities';
import {
  getGatewayCapabilities,
  normalizeGatewayCapability,
  writeGatewayCapability,
  type GatewayCapabilityRecord,
} from './api';

const row: GatewayCapabilityRecord = {
  id: '1',
  revision: '2',
  updated_at: 1800000000,
  base_url: 'https://gateway.example/v3',
  model: 'provider/model',
  adapter: 'openai_responses',
  efforts: ['none', 'max'],
  max_output_tokens: 0,
  storage: 'openai',
  cache: 'reject',
};
describe('Gateway capability API', () => {
  it('reads a valid declared URL longer than 4096 characters', async () => {
    const value = { ...row, base_url: 'https://gateway.example/' + 'a'.repeat(5000) };
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => new Response(JSON.stringify({ data: [value] }))),
    );
    await expect(getGatewayCapabilities()).resolves.toEqual([value]);
  });
  it('preserves an explicit zero output ceiling and complete target identity', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => new Response(JSON.stringify({ data: [row] }))),
    );
    await expect(getGatewayCapabilities()).resolves.toEqual([row]);
    expect(gatewayAdapterEfforts('openai_chat')).not.toContain('max');
    expect(gatewayAdapterEfforts('anthropic_adaptive')).not.toContain('xhigh');
    expect(gatewayAdapterEfforts('anthropic_always_adaptive')).toContain('xhigh');
  });
  it('treats an invalid successful mutation response as unknown instead of confirming it', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(
        async () =>
          new Response(JSON.stringify({ id: '1', deleted: false }), {
            headers: { 'Content-Type': 'application/json' },
          }),
      ),
    );
    await expect(
      writeGatewayCapability(
        { action: 'delete', id: '1', expected_revision: '2' },
        'operation-key',
      ),
    ).rejects.toMatchObject({ code: 'invalid_response' });
    expect(() => normalizeGatewayCapability({ ...row, max_output_tokens: null })).toThrow();
  });
});
