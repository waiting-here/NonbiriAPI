import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { productionAccountLifecycleAdapter } from './adapters';

function fixture(path: string): unknown {
  return JSON.parse(readFileSync(resolve(process.cwd(), '..', path), 'utf8')) as unknown;
}

function exportDocument(): Record<string, unknown> {
  return {
    schema_version: 11,
    generated_at: 1_700_000_000,
    user: { id: '1' },
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
    request_adaptations: [],
    continuity: [],
    fat_fish: { summaries: [], progress: [] },
  };
}

function fishID(prefix: string, ordinal = 0): string {
  return `${prefix}${String(ordinal).padStart(21, '0')}A`;
}

function fishSummary(): Record<string, unknown> {
  return {
    id: fishID('ffc_'),
    period_id: fishID('ffp_'),
    node_id: fishID('ffn_'),
    version_id: fishID('ffv_'),
    engine_version: 1,
    scoring_version: 1,
    state: 'settled_pass',
    prepared_at_ms: 1_699_999_995_000,
    started_at_ms: 1_699_999_996_000,
    completed_at_ms: 1_699_999_999_000,
    passed: true,
    stars: 2,
    score_units: '12345',
    ticket_charge: '1.500',
    ticket_refund: '0',
    rewards: '2.000',
    seed_commit: 'a'.repeat(64),
    commitment_verified: true,
  };
}

function fishProgress(ordinal = 0): Record<string, unknown> {
  return {
    period_id: fishID('ffp_'),
    node_id: fishID('ffn_', ordinal),
    unlocked_at: 1_699_999_900,
    unlock_operation_id: fishID('op_'),
    passed: true,
    best_stars: 2,
    best_score_units: '12345',
    best_at_ms: 1_699_999_999_000,
    best_version_id: fishID('ffv_'),
  };
}

function populatedExportDocument(): Record<string, unknown> {
  return {
    ...exportDocument(),
    endpoints: [{ id: '1' }],
    request_adaptations: [
      {
        endpoint_id: '1',
        revision: '2',
        forward_headers: ['x-correlation-id'],
        fixed_headers: [{ path: 'x-upstream-tenant', has_value: true }],
        body_defaults: [{ path: '/temperature', has_value: true }],
        body_forced: [{ path: '/store', has_value: true }],
        native_extension_paths: ['/thinking'],
      },
    ],
    continuity: [
      {
        kind: 'checkin_general',
        scope: 'daily',
        window: '2026-09-27',
        state: 'completed',
        expires_at: 1_700_000_100,
      },
      {
        kind: 'game_onboarding',
        scope: 'game',
        window: 'once',
        state: 'completed',
        expires_at: null,
      },
    ],
    fat_fish: { summaries: [fishSummary()], progress: [fishProgress()] },
  };
}

function exportResponse(body: unknown, headers: HeadersInit = {}): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: {
      'Content-Type': 'application/json; charset=utf-8',
      'Content-Disposition': 'attachment; filename="nonbiriapi-account-export-v11.json"',
      ...headers,
    },
  });
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('production account lifecycle adapter', () => {
  it('downloads one bounded schema-v11 attachment with only the elevated capability header', async () => {
    const document = exportDocument();
    const fetchMock = vi.fn<typeof fetch>(async () => exportResponse(document));
    vi.stubGlobal('fetch', fetchMock);

    const attachment = await productionAccountLifecycleAdapter.exportAccount({
      accountId: '1',
      elevatedToken: 'elevated_token',
    });

    expect(attachment.schemaVersion).toBe(11);
    expect(JSON.parse(await attachment.blob.text())).toEqual(document);
    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [path, init] = fetchMock.mock.calls[0] ?? [];
    expect(path).toBe('/api/account/export');
    expect(init?.method).toBe('POST');
    expect(init?.body).toBeUndefined();
    expect(new Headers(init?.headers).get('X-Elevated-Token')).toBe('elevated_token');
  });

  it('accepts populated safe v11 domain projections without disclosing secret values', async () => {
    const document = populatedExportDocument();
    const fetchMock = vi.fn<typeof fetch>(async () => exportResponse(document));
    vi.stubGlobal('fetch', fetchMock);

    const attachment = await productionAccountLifecycleAdapter.exportAccount({
      accountId: '1',
      elevatedToken: 'elevated_token',
    });

    expect(attachment.schemaVersion).toBe(11);
    expect(JSON.parse(await attachment.blob.text())).toEqual(document);
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it('accepts nullable fish start, unlock operation and best-result fields', async () => {
    const document = populatedExportDocument();
    const fish = document.fat_fish as Record<string, Record<string, unknown>[]>;
    Object.assign(fish.summaries[0], {
      state: 'expired',
      started_at_ms: null,
      passed: false,
      stars: 0,
      score_units: '0',
      ticket_charge: '0',
      ticket_refund: '0',
      rewards: '0',
      commitment_verified: false,
    });
    Object.assign(fish.progress[0], {
      unlock_operation_id: null,
      passed: false,
      best_stars: 0,
      best_score_units: '0',
      best_at_ms: null,
      best_version_id: null,
    });
    vi.stubGlobal(
      'fetch',
      vi.fn<typeof fetch>(async () => exportResponse(document)),
    );

    const attachment = await productionAccountLifecycleAdapter.exportAccount({
      accountId: '1',
      elevatedToken: 'elevated_token',
    });
    expect(JSON.parse(await attachment.blob.text())).toEqual(document);
  });

  it('exports ten private custom presets with exact revisions and ordered skills', async () => {
    const document = exportDocument();
    document.likes = {
      loadouts: Array.from({ length: 10 }, (_, index) => ({
        slot: index + 1,
        revision: '9007199254740993',
        mode: index % 2 ? 'standard' : 'quick',
        loadout: { role: 'tank', harness: index % 2 ? 'armor' : null, skills: ['guard', 'strike'] },
        updated_at: 1_699_999_900,
      })),
    };
    vi.stubGlobal(
      'fetch',
      vi.fn<typeof fetch>(async () => exportResponse(document)),
    );
    const attachment = await productionAccountLifecycleAdapter.exportAccount({
      accountId: '1',
      elevatedToken: 'elevated_token',
    });
    expect(JSON.parse(await attachment.blob.text())).toEqual(document);
  });

  it.each(['abandoned', 'cancelled_refunded'])(
    'accepts a %s receipt terminated during the countdown',
    async (state) => {
      const document = populatedExportDocument();
      const fish = document.fat_fish as Record<string, Record<string, unknown>[]>;
      Object.assign(fish.summaries[0], {
        state,
        started_at_ms: 1_699_999_998_000,
        completed_at_ms: 1_699_999_996_000,
        passed: false,
        stars: 0,
        score_units: '0',
        ticket_refund: state === 'cancelled_refunded' ? '1.5' : '0',
        rewards: '0',
        commitment_verified: false,
      });
      vi.stubGlobal(
        'fetch',
        vi.fn<typeof fetch>(async () => exportResponse(document)),
      );
      const result = await productionAccountLifecycleAdapter.exportAccount({
        accountId: '1',
        elevatedToken: 'elevated_token',
      });
      expect(result.schemaVersion).toBe(11);
    },
  );

  it('rejects malformed, wrong-version and oversized successful exports without retrying', async () => {
    const fetchMock = vi.fn<typeof fetch>();
    fetchMock
      .mockResolvedValueOnce(exportResponse({ ...exportDocument(), schema_version: '11' }))
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

  it.each([12, 13])(
    'downloads a v%s attachment and rejects a version mismatch in its filename',
    async (version) => {
      const document = {
        ...exportDocument(),
        schema_version: version,
        lake_notes: { server_projection: true },
      };
      const fetchMock = vi
        .fn<typeof fetch>()
        .mockResolvedValueOnce(
          exportResponse(document, {
            'Content-Disposition': `attachment; filename="nonbiriapi-account-export-v${version}.json"`,
          }),
        )
        .mockResolvedValueOnce(exportResponse(document));
      vi.stubGlobal('fetch', fetchMock);
      const result = await productionAccountLifecycleAdapter.exportAccount({
        accountId: '1',
        elevatedToken: 'elevated_token',
      });
      expect(result.schemaVersion).toBe(version);
      expect(JSON.parse(await result.blob.text())).toEqual(document);
      await expect(
        productionAccountLifecycleAdapter.exportAccount({
          accountId: '1',
          elevatedToken: 'elevated_token',
        }),
      ).rejects.toMatchObject({ code: 'invalid_response' });
      expect(fetchMock).toHaveBeenCalledTimes(2);
    },
  );

  it('rejects the previous export schema and filename without retrying', async () => {
    const previousDocument = exportDocument();
    previousDocument.schema_version = 10;
    delete previousDocument.request_adaptations;
    delete previousDocument.continuity;
    delete previousDocument.fat_fish;
    const fetchMock = vi.fn<typeof fetch>();
    fetchMock.mockResolvedValueOnce(exportResponse(previousDocument)).mockResolvedValueOnce(
      exportResponse(exportDocument(), {
        'Content-Disposition': 'attachment; filename="nonbiriapi-account-export-v10.json"',
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

  it('binds the attachment to the current account but leaves domain interpretation to the server', async () => {
    const document = populatedExportDocument();
    document.likes = { loadouts: [{ revision: '9007199254740993', name: 'Saved preset' }] };
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(exportResponse(document))
      .mockResolvedValueOnce(exportResponse({ ...document, user: { id: '2' } }));
    vi.stubGlobal('fetch', fetchMock);
    const result = await productionAccountLifecycleAdapter.exportAccount({
      accountId: '1',
      elevatedToken: 'elevated_token',
    });
    expect(JSON.parse(await result.blob.text())).toEqual(document);
    await expect(
      productionAccountLifecycleAdapter.exportAccount({
        accountId: '1',
        elevatedToken: 'elevated_token',
      }),
    ).rejects.toMatchObject({ code: 'invalid_response' });
  });

  it('rejects invalid UTF-8 and malformed JSON without producing an attachment', async () => {
    const headers = {
      'Content-Type': 'application/json',
      'Content-Disposition': 'attachment; filename="nonbiriapi-account-export-v11.json"',
    };
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(new Response(new Uint8Array([255]), { headers }))
      .mockResolvedValueOnce(new Response('{', { headers }));
    vi.stubGlobal('fetch', fetchMock);
    for (let index = 0; index < 2; index++) {
      await expect(
        productionAccountLifecycleAdapter.exportAccount({
          accountId: '1',
          elevatedToken: 'elevated_token',
        }),
      ).rejects.toMatchObject({ code: 'invalid_response' });
    }
  });

  it('cancels an oversized streamed attachment before parsing or downloading', async () => {
    const cancel = vi.fn();
    const body = new ReadableStream<Uint8Array>({
      start(controller) {
        controller.enqueue(new Uint8Array(16 * 1024 * 1024 + 1));
      },
      cancel,
    });
    vi.stubGlobal(
      'fetch',
      vi.fn<typeof fetch>(
        async () =>
          new Response(body, {
            headers: {
              'Content-Type': 'application/json',
              'Content-Disposition': 'attachment; filename="nonbiriapi-account-export-v11.json"',
            },
          }),
      ),
    );
    await expect(
      productionAccountLifecycleAdapter.exportAccount({
        accountId: '1',
        elevatedToken: 'elevated_token',
      }),
    ).rejects.toMatchObject({ code: 'invalid_response' });
    expect(cancel).toHaveBeenCalledOnce();
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
