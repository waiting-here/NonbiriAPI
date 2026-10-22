import { afterEach, describe, expect, it, vi } from 'vitest';
import type { MainstreamEntry } from '@shared/connections';
import { createAdminMainstreamChannel, patchAdminMainstreamChannel } from './channels';
const id = 'mch_' + 'A'.repeat(22);
const entry: MainstreamEntry = {
  name: 'Messages',
  connector_type: 'anthropic-compatible',
  base_url: 'https://messages.example.test/v1',
  native_services: ['anthropic_messages'],
  rerank_format: '',
  enabled: true,
};
function strictChannelServer() {
  const writes: { method: string; body: Record<string, unknown> }[] = [];
  const fetch = vi.fn(async (_input: RequestInfo | URL, options?: RequestInit) => {
    const body = JSON.parse(String(options?.body ?? '{}')) as Record<string, unknown>;
    writes.push({ method: options?.method ?? 'GET', body });
    const mixed =
      Object.hasOwn(body, 'entries') &&
      (Object.hasOwn(body, 'connector_type') || Object.hasOwn(body, 'base_url'));
    if (mixed)
      return new Response(
        JSON.stringify({ error: { code: 'invalid_request', message: 'Use one channel layout.' } }),
        { status: 400, headers: { 'content-type': 'application/json' } },
      );
    const entries = Array.isArray(body.entries)
      ? body.entries
      : [
          {
            ...entry,
            connector_type: body.connector_type ?? 'openai-compatible',
            base_url: body.base_url ?? 'https://legacy.example.test/v1',
            native_services: ['chat_completions', 'responses'],
          },
        ];
    const first = entries[0] as Record<string, unknown>;
    const projection = {
      id,
      name: body.name ?? 'Channel',
      category: 'subscription',
      enabled: true,
      state: 'active',
      revision: '2',
      created_at: 1700000000,
      updated_at: 1700000001,
      retired_at: null,
      discovery_source: null,
      model_mappings: [],
      connector_type: first.connector_type,
      base_url: first.base_url,
      entries: entries.map((row: Record<string, unknown>) => ({ ...row, id: '1' })),
    };
    return new Response(JSON.stringify(projection), {
      status: options?.method === 'POST' ? 201 : 200,
      headers: { 'content-type': 'application/json' },
    });
  });
  vi.stubGlobal('fetch', fetch);
  return { fetch, writes };
}
afterEach(() => vi.unstubAllGlobals());
describe('mainstream channel request layout compatibility', () => {
  it('creates and edits entries without sending their legacy projection fields', async () => {
    const server = strictChannelServer();
    const layout = { entries: [entry], discovery_source: null, model_mappings: [] };
    await createAdminMainstreamChannel(
      {
        name: 'Channel',
        category: 'subscription',
        enabled: true,
        connector_type: 'openai-compatible',
        base_url: '',
        ...layout,
      },
      'entries-create',
    );
    await patchAdminMainstreamChannel(
      id,
      {
        expected_revision: '2',
        entries: [{ ...entry, id: '1', base_url: 'https://updated.example.test/v1' }],
        connector_type: 'anthropic-compatible',
        base_url: 'https://updated.example.test/v1',
      },
      'entries-patch',
    );
    expect(server.writes).toEqual([
      {
        method: 'POST',
        body: { name: 'Channel', category: 'subscription', enabled: true, ...layout },
      },
      {
        method: 'PATCH',
        body: {
          expected_revision: '2',
          entries: [{ ...entry, id: '1', base_url: 'https://updated.example.test/v1' }],
        },
      },
    ]);
    const rejected = await server.fetch('/admin/api/mainstream-channels', {
      method: 'POST',
      body: JSON.stringify({
        entries: [entry],
        connector_type: 'openai-compatible',
        base_url: 'https://mixed.example.test/v1',
      }),
    });
    expect(rejected.status).toBe(400);
  });
  it('retains legacy single-entry create and patch bodies', async () => {
    const server = strictChannelServer();
    const legacy = {
      name: 'Legacy',
      category: 'subscription' as const,
      enabled: true,
      connector_type: 'openai-compatible' as const,
      base_url: 'https://legacy.example.test/v1',
    };
    await createAdminMainstreamChannel(legacy, 'legacy-create');
    const patch = {
      expected_revision: '2',
      base_url: 'https://legacy-updated.example.test/v1',
      connector_type: 'openai-compatible' as const,
    };
    await patchAdminMainstreamChannel(id, patch, 'legacy-patch');
    expect(server.writes).toEqual([
      { method: 'POST', body: legacy },
      { method: 'PATCH', body: patch },
    ]);
  });
});
