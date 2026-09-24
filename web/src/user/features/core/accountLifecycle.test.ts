import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { productionAccountLifecycleAdapter } from './adapters';

function fixture(path: string): unknown {
  return JSON.parse(readFileSync(resolve(process.cwd(), '..', path), 'utf8')) as unknown;
}

function exportDocument(): Record<string, unknown> {
  return {
    schema_version: 10,
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
    checkins: [],
    game_onboarding: [],
    game_onboarding_holds: [],
    loans: [],
    game_rankings: { statistics_start: 1_700_000_000, totals: [], events: [] },
    penalties: [],
    welfare_claims: [],
    thursday: [],
    donations: [],
    charity: {},
    fishing: {},
    linklink: {},
    rps: {},
    bidding: {},
    likes: {},
    blackjack: {},
    randomness: [],
    limited_activities: {
      wallet: { general: '0', sketch_paper: '0', sketch_brush: '0' },
      exchanges: [],
    },
    image_tasks: [],
    inactivity: { activity: null, runs: [] },
  };
}

function exportResponse(body: unknown, headers: HeadersInit = {}): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: {
      'Content-Type': 'application/json; charset=utf-8',
      'Content-Disposition': 'attachment; filename="nonbiriapi-account-export-v10.json"',
      ...headers,
    },
  });
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('production account lifecycle adapter', () => {
  it('downloads one bounded schema-v10 attachment with only the elevated capability header', async () => {
    const document = exportDocument();
    const fetchMock = vi.fn<typeof fetch>(async () => exportResponse(document));
    vi.stubGlobal('fetch', fetchMock);

    const attachment = await productionAccountLifecycleAdapter.exportAccount({
      accountId: '1',
      elevatedToken: 'elevated_token',
    });

    expect(attachment.schemaVersion).toBe(10);
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
      .mockResolvedValueOnce(exportResponse({ ...exportDocument(), schema_version: 4 }))
      .mockResolvedValueOnce(
        exportResponse(exportDocument(), { 'Content-Length': String(16 * 1024 * 1024 + 1) }),
      );
    vi.stubGlobal('fetch', fetchMock);

    for (let index = 0; index < 3; index += 1) {
      await expect(
        productionAccountLifecycleAdapter.exportAccount({
          accountId: '1',
          elevatedToken: 'elevated_token',
        }),
      ).rejects.toMatchObject({ code: 'invalid_response' });
    }
    expect(fetchMock).toHaveBeenCalledTimes(3);
  });

  it.each(['limited_activities', 'image_tasks', 'inactivity'])(
    'rejects an export missing the required %s section without retrying',
    async (key) => {
      const document = exportDocument();
      delete document[key];
      const fetchMock = vi.fn<typeof fetch>(async () => exportResponse(document));
      vi.stubGlobal('fetch', fetchMock);

      await expect(
        productionAccountLifecycleAdapter.exportAccount({
          accountId: '1',
          elevatedToken: 'elevated_token',
        }),
      ).rejects.toMatchObject({ code: 'invalid_response' });
      expect(fetchMock).toHaveBeenCalledTimes(1);
    },
  );

  it('rejects the previous export schema and filename without retrying', async () => {
    const previousDocument = exportDocument();
    previousDocument.schema_version = 9;
    delete previousDocument.limited_activities;
    delete previousDocument.image_tasks;
    delete previousDocument.inactivity;
    const fetchMock = vi.fn<typeof fetch>();
    fetchMock.mockResolvedValueOnce(exportResponse(previousDocument)).mockResolvedValueOnce(
      exportResponse(exportDocument(), {
        'Content-Disposition': 'attachment; filename="nonbiriapi-account-export-v9.json"',
      }),
    );
    vi.stubGlobal('fetch', fetchMock);

    for (let index = 0; index < 2; index += 1) {
      await expect(
        productionAccountLifecycleAdapter.exportAccount({
          accountId: '1',
          elevatedToken: 'elevated_token',
        }),
      ).rejects.toMatchObject({ code: 'invalid_response' });
    }
    expect(fetchMock).toHaveBeenCalledTimes(2);
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
      productionAccountLifecycleAdapter.exportAccount({ accountId: '01', elevatedToken: 'short' }),
    ).rejects.toMatchObject({ code: 'invalid_request' });
    expect(fetchMock).not.toHaveBeenCalled();
  });
});
