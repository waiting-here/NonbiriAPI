import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it, vi } from 'vitest';
import {
  createEndpoint,
  createManualEntries,
  getBindingCandidates,
  getCatalog,
  getCallerKey,
  getEndpoint,
  getEndpointCreateOptions,
  getHomeAnnouncements,
  getHomeCheckinStatus,
  getHomeGameSummary,
  getModel,
  listModels,
  orderBindings,
  refreshDiscovery,
  regenerateCallerKey,
  submitHomeCheckin,
  updateManualEntry,
} from './api';
import { coreRequest } from './request';
import type { OperationIdentity } from './types';

const operation: OperationIdentity = {
  idempotencyKey: 'ABCDEFGHIJKLMNOPQRSTUV',
  actionId: 'action-a',
};

function fixture(path: string): unknown {
  return JSON.parse(readFileSync(resolve(process.cwd(), '..', path), 'utf8')) as unknown;
}

function jsonResponse(body: unknown, status: number, headers: HeadersInit = {}): Response {
  return new Response(status === 204 ? null : JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json', ...headers },
  });
}

describe('core API wire contract', () => {
  it('uses the frozen home paths, closed DTOs, and bodyless check-in mutation', async () => {
    const suffix = 'A'.repeat(22);
    const announcement = {
      epoch: `b1e_${suffix}`,
      id: `ann_${suffix}`,
      revision: '1',
      severity: 'info',
      pinned: false,
      dismissible: true,
      published_at: 1_700_000_000,
      expires_at: null,
      effective_language: 'en',
      fallback_from: null,
      title: 'Confirmed notice',
      excerpt: 'A bounded excerpt.',
    };
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(
        jsonResponse(
          {
            enabled: true,
            checked_in_today: false,
            balance: '-1.5',
            award_min: '1',
            award_max: '2',
            balance_cap: '340282366920938463463374607431768211.455',
          },
          200,
        ),
      )
      .mockResolvedValueOnce(jsonResponse({ award: '2', balance: '0.5' }, 200))
      .mockResolvedValueOnce(
        jsonResponse(
          {
            continue: [
              {
                game: 'linklink',
                resource_id: `ll_${suffix}`,
                state: 'active',
                route_id: 'game-linklink',
              },
            ],
            pending_results: [],
          },
          200,
        ),
      )
      .mockResolvedValueOnce(jsonResponse({ data: [announcement], next_cursor: null }, 200));
    vi.stubGlobal('fetch', fetchMock);

    await expect(getHomeCheckinStatus()).resolves.toMatchObject({ balance: '-1.5' });
    await expect(submitHomeCheckin()).resolves.toEqual({ award: '2', balance: '0.5' });
    await expect(getHomeGameSummary()).resolves.toEqual([
      expect.objectContaining({ game: 'linklink', kind: 'continue' }),
    ]);
    await expect(getHomeAnnouncements()).resolves.toEqual([
      { id: announcement.id, title: announcement.title, excerpt: announcement.excerpt },
    ]);

    expect(fetchMock.mock.calls.map(([path]) => path)).toEqual([
      '/api/checkin',
      '/api/checkin',
      '/api/home/game-summary',
      '/api/announcements?limit=20',
    ]);
    const [, post] = fetchMock.mock.calls[1] ?? [];
    expect(post?.method).toBe('POST');
    expect(post?.body).toBeUndefined();
    expect(new Headers(post?.headers).has('Content-Type')).toBe(false);
    expect(new Headers(post?.headers).has('Idempotency-Key')).toBe(false);
  });

  it('sends control mutations with one idempotency identity and exact body', async () => {
    const fetchMock = vi.fn<typeof fetch>(async () =>
      jsonResponse(fixture('internal/resources/testdata/endpoint.json'), 201),
    );
    vi.stubGlobal('fetch', fetchMock);

    await createEndpoint(
      {
        source: 'custom',
        connector_type: 'openai-compatible',
        base_url: 'https://example.com/v1',
        note: 'endpoint note',
        enabled: true,
      },
      operation,
    );

    const [path, init] = fetchMock.mock.calls[0] ?? [];
    expect(path).toBe('/api/endpoints');
    expect(init?.method).toBe('POST');
    expect(new Headers(init?.headers).get('Idempotency-Key')).toBe(operation.idempotencyKey);
    expect(JSON.parse(String(init?.body))).toEqual({
      source: 'custom',
      connector_type: 'openai-compatible',
      base_url: 'https://example.com/v1',
      note: 'endpoint note',
      enabled: true,
    });
  });

  it('rejects non-closed mutation input before any network request', async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);
    const input = {
      source: 'custom' as const,
      connector_type: 'openai-compatible' as const,
      base_url: 'https://example.com/v1',
      note: 'endpoint note',
      enabled: true,
      extra: 'must not cross the wire',
    };

    await expect(createEndpoint(input, operation)).rejects.toMatchObject({
      code: 'invalid_request',
    });
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('sends the mainstream union without client connector or URL fields', async () => {
    const fetchMock = vi.fn<typeof fetch>(async () =>
      jsonResponse(fixture('internal/resources/testdata/endpoint.json'), 201),
    );
    vi.stubGlobal('fetch', fetchMock);
    const channelID = `mch_${'A'.repeat(22)}`;

    await createEndpoint(
      { source: 'mainstream', channel_id: channelID, note: 'channel note', enabled: true },
      operation,
    );

    expect(JSON.parse(String(fetchMock.mock.calls[0]?.[1]?.body))).toEqual({
      source: 'mainstream',
      channel_id: channelID,
      note: 'channel note',
      enabled: true,
    });
  });

  it('normalizes endpoint creation options and rejects extra option fields', async () => {
    const channelID = `mch_${'B'.repeat(21)}A`;
    const fetchMock = vi.fn<typeof fetch>(async () =>
      jsonResponse(
        {
          base_connector_types: ['openai-compatible', 'anthropic-compatible'],
          mainstream_channels: [
            {
              id: channelID,
              name: 'Main channel',
              connector_type: 'openai-compatible',
              base_url: 'https://example.com/v1',
            },
          ],
        },
        200,
      ),
    );
    vi.stubGlobal('fetch', fetchMock);

    await expect(getEndpointCreateOptions()).resolves.toEqual({
      base_connector_types: ['openai-compatible', 'anthropic-compatible'],
      mainstream_channels: [
        {
          id: channelID,
          name: 'Main channel',
          connector_type: 'openai-compatible',
          base_url: 'https://example.com/v1',
        },
      ],
    });

    fetchMock.mockResolvedValueOnce(
      jsonResponse(
        {
          base_connector_types: ['openai-compatible'],
          mainstream_channels: [
            {
              id: channelID,
              name: 'Main channel',
              connector_type: 'openai-compatible',
              base_url: 'https://example.com/v1',
              revision: '1',
            },
          ],
        },
        200,
      ),
    );
    await expect(getEndpointCreateOptions()).rejects.toMatchObject({ code: 'invalid_response' });
  });

  it('requires CallerKey generation evidence and never adds an idempotency header to plaintext generation', async () => {
    const secret = `nbk_${'A'.repeat(43)}`;
    const metadata = {
      display: 'nbk_AAAA…AAAA',
      created_at: 1_700_000_000,
      updated_at: 1_700_000_000,
      generation: '1',
    };
    const fetchMock = vi.fn<typeof fetch>(async () => jsonResponse(null, 500));
    fetchMock
      .mockResolvedValueOnce(jsonResponse(null, 200, { 'X-Nonbiri-CallerKey-Generation': '0' }))
      .mockResolvedValueOnce(jsonResponse({ secret, metadata }, 200));
    vi.stubGlobal('fetch', fetchMock);

    expect(await getCallerKey()).toEqual({ generation: '0', metadata: null });
    expect((await regenerateCallerKey('0')).secret).toBe(secret);
    const [, regenerateInit] = fetchMock.mock.calls[1] ?? [];
    expect(new Headers(regenerateInit?.headers).has('Idempotency-Key')).toBe(false);
    expect(JSON.parse(String(regenerateInit?.body))).toEqual({ expected_generation: '0' });
  });

  it('rejects missing generation headers and malformed successful DTOs', async () => {
    const endpoint = fixture('internal/resources/testdata/endpoint.json') as Record<
      string,
      unknown
    >;
    const fetchMock = vi.fn<typeof fetch>(async () => jsonResponse(null, 500));
    fetchMock
      .mockResolvedValueOnce(jsonResponse(null, 200))
      .mockResolvedValueOnce(jsonResponse({ ...endpoint, extra: true }, 200));
    vi.stubGlobal('fetch', fetchMock);

    await expect(getCallerKey()).rejects.toMatchObject({ code: 'invalid_response' });
    await expect(getEndpoint('11')).rejects.toMatchObject({ code: 'invalid_response' });
  });

  it('canonicalizes candidate queries and rejects oversized path IDs before fetch', async () => {
    const fetchMock = vi.fn<typeof fetch>(async () =>
      jsonResponse(fixture('internal/resources/testdata/binding_candidates.json'), 200),
    );
    vi.stubGlobal('fetch', fetchMock);

    await getBindingCandidates('31', { endpointId: '11', keyId: '21', source: 'manual' });
    expect(fetchMock.mock.calls[0]?.[0]).toBe(
      '/api/models/31/binding-candidates?endpoint_id=11&key_id=21&source=manual&limit=50',
    );
    fetchMock.mockClear();
    await expect(getModel('9223372036854775808')).rejects.toMatchObject({
      code: 'invalid_request',
    });
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('enforces the 100-item catalog and model page ceiling on requests', async () => {
    const fetchMock = vi.fn<typeof fetch>(async (input) => {
      const path = String(input);
      if (path.startsWith('/api/models?'))
        return jsonResponse({ data: [], next_cursor: null }, 200);
      if (path.includes('/binding-candidates?')) {
        return jsonResponse(fixture('internal/resources/testdata/binding_candidates.json'), 200);
      }
      return jsonResponse(fixture('internal/resources/testdata/catalog_unknown.json'), 200);
    });
    vi.stubGlobal('fetch', fetchMock);

    await listModels(undefined, undefined, 100);
    await getCatalog('11', '21', undefined, undefined, 100);
    await getBindingCandidates('31', { limit: 100 });
    expect(fetchMock.mock.calls.map(([path]) => path)).toEqual([
      '/api/models?limit=100',
      '/api/endpoints/11/keys/21/models?limit=100',
      '/api/models/31/binding-candidates?limit=100',
    ]);

    fetchMock.mockClear();
    await expect(listModels(undefined, undefined, 101)).rejects.toMatchObject({
      code: 'invalid_request',
    });
    await expect(getCatalog('11', '21', undefined, undefined, 101)).rejects.toMatchObject({
      code: 'invalid_request',
    });
    await expect(getBindingCandidates('31', { limit: 101 })).rejects.toMatchObject({
      code: 'invalid_request',
    });
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('accepts an exact 100-entry manual batch and rejects 101 before fetch', async () => {
    const entries = Array.from({ length: 100 }, (_, index) => ({
      upstream_model_id: `Vendor/Model-${index}`,
      provider: 'Vendor',
    }));
    const response = {
      entries: entries.map((entry, index) => ({
        id: String(index + 1),
        source_type: 'manual',
        ...entry,
        source_revision: '1',
        pair_revision: '1',
        created_at: 1_700_000_003,
        updated_at: 1_700_000_003,
      })),
    };
    const fetchMock = vi.fn<typeof fetch>(async () => jsonResponse(response, 201));
    vi.stubGlobal('fetch', fetchMock);

    expect((await createManualEntries('11', '21', entries, operation)).entries).toHaveLength(100);
    expect(JSON.parse(String(fetchMock.mock.calls[0]?.[1]?.body))).toEqual({ entries });

    fetchMock.mockClear();
    await expect(
      createManualEntries('11', '21', [...entries, entries[0]!], operation),
    ).rejects.toMatchObject({ code: 'invalid_request' });
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('rejects manual receipts that do not authoritatively match the requested batch or entry', async () => {
    const fetchMock = vi.fn<typeof fetch>(async () => jsonResponse({ entries: [] }, 201));
    fetchMock.mockResolvedValueOnce(jsonResponse({ entries: [] }, 201));
    fetchMock.mockResolvedValueOnce(jsonResponse({ entries: [], affected_models: [] }, 200));
    vi.stubGlobal('fetch', fetchMock);

    await expect(
      createManualEntries(
        '11',
        '21',
        [
          {
            upstream_model_id: 'Vendor/Exact',
            provider: 'Vendor',
          },
        ],
        operation,
      ),
    ).rejects.toMatchObject({ code: 'invalid_response' });
    await expect(
      updateManualEntry(
        '11',
        '21',
        '31',
        {
          upstream_model_id: 'Vendor/New',
          provider: 'Vendor',
          expected_pair_revision: '2',
          replacements: [],
        },
        operation,
      ),
    ).rejects.toMatchObject({ code: 'invalid_response' });
  });

  it('requires a monotonic manual pair revision and every requested binding replacement', async () => {
    const canonical = fixture('internal/resources/testdata/manual_update.json') as Record<
      string,
      unknown
    >;
    const entries = canonical.entries as Array<Record<string, unknown>>;
    const accepted = {
      ...canonical,
      entries: [{ ...entries[0], pair_revision: '3' }],
    };
    const missingReplacement = { ...accepted, affected_models: [] };
    const staleRevision = {
      ...accepted,
      entries: [{ ...entries[0], pair_revision: '2' }],
    };
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(jsonResponse(accepted, 200))
      .mockResolvedValueOnce(jsonResponse(missingReplacement, 200))
      .mockResolvedValueOnce(jsonResponse(staleRevision, 200));
    vi.stubGlobal('fetch', fetchMock);
    const input = {
      upstream_model_id: 'Vendor/New',
      provider: 'Vendor',
      expected_pair_revision: '2',
      replacements: [{ binding_id: '51', replacement_upstream_model_id: 'Vendor/New' }],
    };

    await expect(updateManualEntry('11', '21', '31', input, operation)).resolves.toEqual(accepted);
    await expect(updateManualEntry('11', '21', '31', input, operation)).rejects.toMatchObject({
      code: 'invalid_response',
    });
    await expect(updateManualEntry('11', '21', '31', input, operation)).rejects.toMatchObject({
      code: 'invalid_response',
    });
  });

  it('rejects resource projections that do not match the requested path identity', async () => {
    const endpoint = fixture('internal/resources/testdata/endpoint.json') as Record<
      string,
      unknown
    >;
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => jsonResponse({ ...endpoint, id: '12' }, 200)),
    );

    await expect(getEndpoint('11')).rejects.toMatchObject({ code: 'invalid_response' });
  });

  it('requires a discovery acceptance to project the checking state', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () =>
        jsonResponse(
          {
            operation_id: `op_${'A'.repeat(22)}`,
            evidence: {
              state: 'succeeded',
              revision: '2',
              result: 'empty',
              safe_class: 'none',
              observed_at: 1_700_000_000,
              count: '0',
            },
          },
          202,
        ),
      ),
    );

    await expect(refreshDiscovery('11', '21', operation)).rejects.toMatchObject({
      code: 'invalid_response',
    });
  });

  it('requires the binding order response to match the requested complete order', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => jsonResponse(fixture('internal/resources/testdata/bindings.json'), 200)),
    );

    await expect(orderBindings('31', '5', ['52'], operation)).rejects.toMatchObject({
      code: 'invalid_response',
    });
  });

  it('preserves AbortError so abandoned reads do not become visible network failures', async () => {
    const controller = new AbortController();
    const abortError = new DOMException('aborted', 'AbortError');
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => {
        throw abortError;
      }),
    );
    controller.abort();
    await expect(coreRequest('/api/me', { signal: controller.signal })).rejects.toBe(abortError);
  });
});
