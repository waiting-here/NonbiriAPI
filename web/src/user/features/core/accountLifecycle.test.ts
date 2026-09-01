import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { productionAccountLifecycleAdapter } from './adapters';

function fixture(path: string): unknown {
  return JSON.parse(readFileSync(resolve(process.cwd(), '..', path), 'utf8')) as unknown;
}

function exportDocument(): Record<string, unknown> {
  return {
    schema_version: 4,
    generated_at: 1_700_000_000,
    user: {},
    endpoints: [],
    catalog_pairs: [],
    models: [],
    caller_key: null,
    usage: {},
    log_summary: {},
    issues: [],
    credit_ledger: [],
    welfare_claims: [],
    thursday: [],
    donations: [],
    charity: {},
    fishing: {},
    linklink: {},
    rps: {},
  };
}

function exportResponse(body: unknown, headers: HeadersInit = {}): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: {
      'Content-Type': 'application/json; charset=utf-8',
      'Content-Disposition': 'attachment; filename="nonbiriapi-account-export-v4.json"',
      ...headers,
    },
  });
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('production account lifecycle adapter', () => {
  it('downloads one bounded schema-v4 attachment with only the elevated capability header', async () => {
    const document = exportDocument();
    const fetchMock = vi.fn<typeof fetch>(async () => exportResponse(document));
    vi.stubGlobal('fetch', fetchMock);

    const attachment = await productionAccountLifecycleAdapter.exportV4({
      accountId: '1',
      elevatedToken: 'elevated_token',
    });

    expect(attachment.schemaVersion).toBe(4);
    expect(JSON.parse(await attachment.blob.text())).toEqual(document);
    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [path, init] = fetchMock.mock.calls[0] ?? [];
    expect(path).toBe('/api/account/export');
    expect(init?.method).toBe('POST');
    expect(init?.body).toBeUndefined();
    expect(new Headers(init?.headers).get('X-Elevated-Token')).toBe('elevated_token');
  });

  it('rejects malformed, wrong-version and oversized successful exports without retrying', async () => {
    const fetchMock = vi.fn<typeof fetch>();
    fetchMock
      .mockResolvedValueOnce(exportResponse({ ...exportDocument(), extra: true }))
      .mockResolvedValueOnce(exportResponse({ ...exportDocument(), schema_version: 3 }))
      .mockResolvedValueOnce(
        exportResponse(exportDocument(), { 'Content-Length': String(16 * 1024 * 1024 + 1) }),
      );
    vi.stubGlobal('fetch', fetchMock);

    for (let index = 0; index < 3; index += 1) {
      await expect(
        productionAccountLifecycleAdapter.exportV4({
          accountId: '1',
          elevatedToken: 'elevated_token',
        }),
      ).rejects.toMatchObject({ code: 'invalid_response' });
    }
    expect(fetchMock).toHaveBeenCalledTimes(3);
  });

  it('submits exact DELETE once and accepts only a 204 response', async () => {
    const fetchMock = vi.fn<typeof fetch>(async () => new Response(null, { status: 204 }));
    vi.stubGlobal('fetch', fetchMock);

    await productionAccountLifecycleAdapter.deleteAccount({
      accountId: '1',
      elevatedToken: 'elevated_token',
      confirmation: 'DELETE',
    });

    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [path, init] = fetchMock.mock.calls[0] ?? [];
    expect(path).toBe('/api/account/delete');
    expect(init?.method).toBe('POST');
    expect(new Headers(init?.headers).get('X-Elevated-Token')).toBe('elevated_token');
    expect(JSON.parse(String(init?.body))).toEqual({ confirm: 'DELETE' });
  });

  it('uses the current session as the explicit post-unknown authority read', async () => {
    const fetchMock = vi.fn<typeof fetch>();
    fetchMock
      .mockResolvedValueOnce(
        new Response(JSON.stringify(fixture('internal/auth/testdata/user_envelope.json')), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        }),
      )
      .mockResolvedValueOnce(
        new Response(
          JSON.stringify({ error: { code: 'unauthorized', message: 'login required' } }),
          {
            status: 401,
            headers: { 'Content-Type': 'application/json' },
          },
        ),
      );
    vi.stubGlobal('fetch', fetchMock);

    await expect(productionAccountLifecycleAdapter.readAccountAuthority('1')).resolves.toBe(
      'active',
    );
    await expect(productionAccountLifecycleAdapter.readAccountAuthority('1')).resolves.toBe(
      'deleted',
    );
    expect(fetchMock.mock.calls.map(([path]) => path)).toEqual(['/api/session', '/api/session']);
  });

  it('rejects malformed local identity and capability material before fetch', async () => {
    const fetchMock = vi.fn<typeof fetch>();
    vi.stubGlobal('fetch', fetchMock);

    await expect(
      productionAccountLifecycleAdapter.exportV4({ accountId: '01', elevatedToken: 'short' }),
    ).rejects.toMatchObject({ code: 'invalid_request' });
    expect(fetchMock).not.toHaveBeenCalled();
  });
});
