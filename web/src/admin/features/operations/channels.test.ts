import { describe, expect, it, vi } from 'vitest';
import {
  createAdminMainstreamChannel,
  getAdminMainstreamChannels,
  normalizeAdminMainstreamChannel,
  patchAdminMainstreamChannel,
  retireAdminMainstreamChannel,
  type AdminMainstreamChannel,
} from './channels';

const channelID = `mch_${'A'.repeat(22)}`;

const active: AdminMainstreamChannel = {
  id: channelID,
  name: 'Example channel',
  category: 'subscription',
  connector_type: 'openai-compatible',
  base_url: 'https://api.example.test/v1',
  enabled: true,
  state: 'active',
  revision: '3',
  created_at: 1,
  updated_at: 2,
  retired_at: null,
};

const retired: AdminMainstreamChannel = {
  ...active,
  name: 'Retired channel',
  enabled: false,
  state: 'retired',
  revision: '4',
  retired_at: 5,
};

function jsonResponse(value: unknown, status = 200): Response {
  return new Response(JSON.stringify(value), {
    status,
    headers: { 'content-type': 'application/json' },
  });
}

describe('administrator mainstream channel wire', () => {
  it('accepts active and retired authority states', () => {
    expect(normalizeAdminMainstreamChannel(active)).toEqual(active);
    expect(normalizeAdminMainstreamChannel(retired)).toEqual(retired);
  });

  it('rejects inconsistent retired state and unknown fields', () => {
    expect(() => normalizeAdminMainstreamChannel({ ...active, retired_at: 9 })).toThrow(
      /retirement state/i,
    );
    expect(() => normalizeAdminMainstreamChannel({ ...retired, enabled: true })).toThrow(
      /retired mainstream channel state/i,
    );
    expect(() => normalizeAdminMainstreamChannel({ ...active, secret: 'never-render' })).toThrow(
      /administrator mainstream channel/i,
    );
    expect(() => normalizeAdminMainstreamChannel({ ...active, name: 'bad\u0080name' })).toThrow(
      /mainstream channel name/i,
    );
    expect(() => normalizeAdminMainstreamChannel({ ...active, base_url: 'https://bad\u0080.example' })).toThrow(
      /canonical URL/i,
    );
  });

  it('decodes a bounded list and preserves cursor validation', async () => {
    const fetchMock = vi.fn<typeof fetch>(async () =>
      jsonResponse({ data: [active], next_cursor: 'YWJj' }),
    );
    vi.stubGlobal('fetch', fetchMock);

    await expect(getAdminMainstreamChannels('active', null, 50)).resolves.toEqual({
      data: [active],
      next_cursor: 'YWJj',
    });
    expect(String(fetchMock.mock.calls[0]?.[0])).toBe(
      '/admin/api/mainstream-channels?state=active&limit=50',
    );
  });

  it('sends strict mutation bodies with a retained idempotency header', async () => {
    const requests: { method: string; body: string | null; headers: Headers }[] = [];
    const fetchMock = vi.fn<typeof fetch>(async (_input, init) => {
      const method = String(init?.method ?? 'GET').toUpperCase();
      requests.push({
        method,
        body: typeof init?.body === 'string' ? init.body : null,
        headers: new Headers(init?.headers),
      });
      if (method === 'DELETE') return new Response(null, { status: 204 });
      return jsonResponse(active, method === 'POST' ? 201 : 200);
    });
    vi.stubGlobal('fetch', fetchMock);

    await expect(
      createAdminMainstreamChannel(
        {
          name: 'New channel',
          category: 'api_platform',
          connector_type: 'anthropic-compatible',
          base_url: 'https://api.example.test/v1',
          enabled: true,
        },
        'create-operation-key',
      ),
    ).resolves.toEqual(active);
    await expect(
      patchAdminMainstreamChannel(
        channelID,
        { expected_revision: '3', enabled: false },
        'patch-operation-key',
      ),
    ).resolves.toEqual(active);
    await expect(retireAdminMainstreamChannel(channelID, '3', 'retire-operation-key')).resolves.toBeUndefined();

    expect(requests).toHaveLength(3);
    const createBody = JSON.parse(requests[0].body ?? 'null') as Record<string, unknown>;
    expect(createBody).toEqual({
      name: 'New channel',
      category: 'api_platform',
      connector_type: 'anthropic-compatible',
      base_url: 'https://api.example.test/v1',
      enabled: true,
    });
    expect(requests[0].headers.get('Idempotency-Key')).toBe('create-operation-key');
    expect(JSON.parse(requests[1].body ?? 'null')).toEqual({ expected_revision: '3', enabled: false });
    expect(JSON.parse(requests[2].body ?? 'null')).toEqual({ expected_revision: '3', confirmation: 'retire' });
    expect(requests[2].headers.get('Idempotency-Key')).toBe('retire-operation-key');
  });

  it('rejects invalid mutation values before issuing a request', () => {
    const fetchMock = vi.fn<typeof fetch>();
    vi.stubGlobal('fetch', fetchMock);
    expect(() =>
      createAdminMainstreamChannel(
        {
          name: '  padded',
          category: 'subscription',
          connector_type: 'openai-compatible',
          base_url: 'https://api.example.test/v1',
          enabled: true,
        },
        'operation-key',
      ),
    ).toThrow(/invalid mainstream channel name/i);
    expect(fetchMock).not.toHaveBeenCalled();
    expect(() =>
      createAdminMainstreamChannel(
        {
          name: 'bad\u0080name',
          category: 'subscription',
          connector_type: 'openai-compatible',
          base_url: 'https://api.example.test/v1',
          enabled: true,
        },
        'operation-key',
      ),
    ).toThrow(/invalid mainstream channel name/i);
  });
});
