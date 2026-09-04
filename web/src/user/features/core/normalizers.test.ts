import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';
import {
  canonicalCandidateFilters,
  normalizeBindingsResponse,
  normalizeCallerKeyAuthority,
  normalizeCallerKeySecret,
  normalizeCatalogView,
  normalizeDiscoveryAccepted,
  normalizeEndpoint,
  normalizeEndpointCreateOptions,
  normalizeEndpointOrigin,
  normalizeEndpointKey,
  normalizeHomeAnnouncementPage,
  normalizeHomeCheckinResult,
  normalizeHomeCheckinStatus,
  normalizeHomeGameSummary,
  normalizeManualUpdateResponse,
  normalizeUserEnvelope,
  validateEndpointSecret,
  validateLogicalName,
  validateManualValue,
  validatePersonalProviderName,
  validateResourceId,
} from './normalizers';

function jsonFixture(path: string): unknown {
  return JSON.parse(readFileSync(resolve(process.cwd(), '..', path), 'utf8')) as unknown;
}

describe('core wire normalizers', () => {
  it('accepts the backend canonical fixtures without converting exact decimals', () => {
    const endpoint = normalizeEndpoint(jsonFixture('internal/resources/testdata/endpoint.json'));
    const key = normalizeEndpointKey(jsonFixture('internal/resources/testdata/endpoint_key.json'));
    const user = normalizeUserEnvelope(jsonFixture('internal/auth/testdata/user_envelope.json'));

    expect(endpoint).toMatchObject({ id: '11', revision: '3', key_count: '2' });
    expect(endpoint.origin).toEqual({ kind: 'custom' });
    expect(key).toMatchObject({ id: '21', endpoint_id: '11', revision: '4' });
    expect(user.user.usage.total_requests).toBe('340282366920938463463374607431768211455');
  });

  it('strictly decodes the frozen home check-in wire without numeric conversion', () => {
    const maximum = '340282366920938463463374607431768211.455';
    expect(normalizeHomeCheckinStatus({ enabled: false })).toEqual({ enabled: false });
    expect(
      normalizeHomeCheckinStatus({
        enabled: true,
        checked_in_today: false,
        balance: '-1.5',
        award_min: '0',
        award_max: maximum,
        balance_cap: maximum,
      }),
    ).toMatchObject({ award_max: maximum, balance_cap: maximum });
    expect(normalizeHomeCheckinResult({ award: maximum, balance: '-1.5' })).toEqual({
      award: maximum,
      balance: '-1.5',
    });

    expect(() => normalizeHomeCheckinStatus({ enabled: false, reason: 'hidden' })).toThrow(
      /check-in status/i,
    );
    expect(() =>
      normalizeHomeCheckinStatus({
        enabled: true,
        checked_in_today: false,
        credits: '1',
        award_min_milli: '1',
        award_max_milli: '2',
        credits_cap_milli: '3',
      }),
    ).toThrow(/check-in status/i);
    expect(() =>
      normalizeHomeCheckinStatus({
        enabled: true,
        checked_in_today: false,
        balance: '1',
        award_min: '2',
        award_max: '1',
        balance_cap: '3',
      }),
    ).toThrow(/award range/i);
    expect(() => normalizeHomeCheckinResult({ award: '1', balance: '2', extra: true })).toThrow(
      /check-in result/i,
    );
  });

  it('enforces the closed game-route-state tuples in the home summary', () => {
    const suffix = 'A'.repeat(22);
    const valid = {
      continue: [
        {
          game: 'fishing',
          resource_id: `fb_${suffix}`,
          state: 'recovery_required',
          route_id: 'game-fishing',
        },
        {
          game: 'linklink',
          resource_id: `ll_${suffix}`,
          state: 'active',
          route_id: 'game-linklink',
        },
        {
          game: 'rps',
          resource_id: `rps_${suffix}`,
          state: 'terminal_processing',
          route_id: 'game-rps',
        },
      ],
      pending_results: [
        {
          game: 'fishing',
          resource_id: `fb_${'B'.repeat(21)}A`,
          created_at: 1_700_000_000,
          route_id: 'game-fishing',
        },
        {
          game: 'rps',
          resource_id: `rps_${'C'.repeat(21)}A`,
          created_at: 1_700_000_001,
          route_id: 'game-rps',
        },
      ],
    };
    const result = normalizeHomeGameSummary(valid);
    expect(result).toHaveLength(5);
    expect(result[0]).toMatchObject({ kind: 'continue', state: 'recovery_required' });
    expect(result[4]).toMatchObject({ kind: 'view', created_at: 1_700_000_001 });

    expect(() =>
      normalizeHomeGameSummary({
        ...valid,
        continue: [{ ...valid.continue[0], route_id: 'game-rps' }],
      }),
    ).toThrow(/home game continuation/i);
    expect(() =>
      normalizeHomeGameSummary({
        ...valid,
        pending_results: [{ ...valid.pending_results[0], state: 'committed' }],
      }),
    ).toThrow(/pending game result/i);
    expect(() => normalizeHomeGameSummary({ ...valid, href: '/games' })).toThrow(
      /home game summary/i,
    );
  });

  it('validates the complete announcement summary before projecting the home card', () => {
    const suffix = 'A'.repeat(22);
    const announcement = {
      epoch: `b1e_${suffix}`,
      id: `ann_${suffix}`,
      revision: '1',
      severity: 'important',
      pinned: true,
      dismissible: false,
      published_at: 1_700_000_000,
      expires_at: null,
      effective_language: 'en',
      fallback_from: null,
      title: 'Long-lived authority',
      excerpt: 'A strict projection.',
    };
    expect(normalizeHomeAnnouncementPage({ data: [announcement], next_cursor: 'next' })).toEqual({
      data: [
        {
          id: announcement.id,
          title: announcement.title,
          excerpt: announcement.excerpt,
        },
      ],
      next_cursor: 'next',
    });
    expect(() =>
      normalizeHomeAnnouncementPage({
        data: [{ ...announcement, rendered_body: 'not a summary field' }],
        next_cursor: null,
      }),
    ).toThrow(/announcement summary/i);
    expect(() =>
      normalizeHomeAnnouncementPage({ data: [announcement], next_cursor: null, extra: true }),
    ).toThrow(/announcements page/i);
  });

  it('rejects unknown fields, unsafe ID widths, C1 controls, and out-of-range times', () => {
    const endpoint = jsonFixture('internal/resources/testdata/endpoint.json') as Record<
      string,
      unknown
    >;
    expect(() => normalizeEndpoint({ ...endpoint, extra: true })).toThrow(/invalid endpoint/i);
    expect(() => normalizeEndpoint({ ...endpoint, id: '9223372036854775808' })).toThrow(
      /endpoint id/i,
    );
    expect(() => normalizeEndpoint({ ...endpoint, note: 'bad\u0085note' })).toThrow(
      /endpoint note/i,
    );
    expect(() =>
      normalizeEndpoint({ ...endpoint, origin: { kind: 'custom', name: 'leak' } }),
    ).toThrow(/endpoint origin/i);
    expect(() => normalizeEndpoint({ ...endpoint, updated_at: 253_402_300_800 })).toThrow(
      /update time/i,
    );
    expect(() =>
      normalizeEndpoint({ ...endpoint, updated_at: Number(endpoint.created_at) - 1 }),
    ).toThrow(/update time/i);
    expect(() => validateResourceId('01', 'endpoint id')).toThrow(/invalid endpoint id/i);
    const user = jsonFixture('internal/auth/testdata/user_envelope.json') as {
      user: { usage: Record<string, unknown> };
    };
    expect(() =>
      normalizeUserEnvelope({
        user: { ...user.user, usage: { ...user.user.usage, total_prompt_tokens: '3703' } },
      }),
    ).toThrow(/usage summary projection/i);
  });

  it('keeps mainstream provenance closed and normalizes only safe creation options', () => {
    const channelID = `mch_${'A'.repeat(22)}`;
    expect(
      normalizeEndpointOrigin({ kind: 'mainstream', channel_id: channelID, name: 'Main channel' }),
    ).toEqual({ kind: 'mainstream', channel_id: channelID, name: 'Main channel' });
    expect(() =>
      normalizeEndpointOrigin({
        kind: 'mainstream',
        channel_id: channelID,
        name: ' '.repeat(128),
      }),
    ).toThrow(/channel name/i);
    expect(() =>
      normalizeEndpointOrigin({
        kind: 'mainstream',
        channel_id: channelID,
        name: 'Main channel',
        category: 'subscription',
      }),
    ).toThrow(/endpoint origin/i);

    expect(
      normalizeEndpointCreateOptions({
        base_connector_types: ['anthropic-compatible', 'openai-compatible'],
        mainstream_channels: [],
      }),
    ).toEqual({
      base_connector_types: ['anthropic-compatible', 'openai-compatible'],
      mainstream_channels: [],
    });
    expect(
      normalizeEndpointCreateOptions({
        base_connector_types: ['openai-compatible', 'anthropic-compatible'],
        mainstream_channels: [],
      }),
    ).toEqual({
      base_connector_types: ['openai-compatible', 'anthropic-compatible'],
      mainstream_channels: [],
    });
    expect(() =>
      normalizeEndpointCreateOptions({
        base_connector_types: ['openai-compatible', 'openai-compatible'],
        mainstream_channels: [],
      }),
    ).toThrow(/base connector types/i);
    expect(() =>
      normalizeEndpointCreateOptions({
        base_connector_types: ['openai-compatible'],
        mainstream_channels: [
          {
            id: channelID,
            name: 'x'.repeat(129),
            connector_type: 'openai-compatible',
            base_url: 'https://example.com/v1',
          },
        ],
      }),
    ).toThrow(/channel option name/i);
  });

  it('enforces the complete discovery evidence matrix', () => {
    expect(
      normalizeCatalogView(jsonFixture('internal/resources/testdata/catalog_unknown.json')).evidence
        .state,
    ).toBe('unknown');
    expect(
      normalizeCatalogView(jsonFixture('internal/resources/testdata/catalog_succeeded_empty.json'))
        .evidence.result,
    ).toBe('empty');

    const invalid = jsonFixture('internal/resources/testdata/catalog_succeeded_empty.json') as {
      evidence: Record<string, unknown>;
    };
    expect(() =>
      normalizeCatalogView({
        ...invalid,
        evidence: { ...invalid.evidence, count: '1' },
      }),
    ).toThrow(/discovery result count/i);
    expect(() =>
      normalizeCatalogView({
        ...invalid,
        evidence: { ...invalid.evidence, safe_class: 'timeout' },
      }),
    ).toThrow(/successful discovery evidence/i);
    expect(() =>
      normalizeCatalogView({
        ...invalid,
        automatic_entries: [
          {
            id: '31',
            source_type: 'automatic',
            upstream_model_id: 'Vendor/Exact',
            provider: 'Vendor',
            source_revision: '7',
            pair_revision: '2',
            created_at: 1_700_000_003,
            updated_at: 1_700_000_013,
          },
        ],
      }),
    ).toThrow(/empty discovery catalog/i);

    const catalog = jsonFixture('internal/resources/testdata/catalog_unknown.json') as Record<
      string,
      unknown
    >;
    const entries = (source_type: 'automatic' | 'manual', count: number, offset: number) =>
      Array.from({ length: count }, (_, index) => ({
        id: String(offset + index + 1),
        source_type,
        upstream_model_id: `Vendor/Model-${offset + index}`,
        provider: 'Vendor',
        source_revision: '1',
        pair_revision: '1',
        created_at: 1_700_000_003,
        updated_at: 1_700_000_003,
      }));
    expect(() =>
      normalizeCatalogView({
        ...catalog,
        automatic_entries: entries('automatic', 51, 0),
        manual_entries: entries('manual', 50, 100),
      }),
    ).toThrow(/catalog page/i);
  });

  it('requires the compact manual update receipt to contain exactly one entry', () => {
    expect(() => normalizeManualUpdateResponse({ entries: [], affected_models: [] })).toThrow(
      /manual catalog update response/i,
    );
    expect(() =>
      normalizeManualUpdateResponse({
        entries: [
          ...(
            jsonFixture('internal/resources/testdata/manual_create.json') as { entries: unknown[] }
          ).entries,
          ...(
            jsonFixture('internal/resources/testdata/manual_create.json') as { entries: unknown[] }
          ).entries,
        ],
        affected_models: [],
      }),
    ).toThrow(/manual catalog update response/i);
  });

  it('accepts only canonical operation IDs and CallerKey plaintext transitions', () => {
    const evidence = {
      state: 'checking',
      revision: '2',
      result: null,
      safe_class: 'none',
      observed_at: 1_700_000_000,
      count: null,
    };
    expect(
      normalizeDiscoveryAccepted({ operation_id: `op_${'A'.repeat(22)}`, evidence }).operation_id,
    ).toHaveLength(25);
    expect(() =>
      normalizeDiscoveryAccepted({ operation_id: `op_${'A'.repeat(21)}B`, evidence }),
    ).toThrow(/operation id/i);

    const metadata = {
      display: 'nbk_AAAA…AAAA',
      created_at: 1_700_000_000,
      updated_at: 1_700_000_000,
      generation: '1',
    };
    expect(normalizeCallerKeyAuthority(metadata, '1').generation).toBe('1');
    expect(
      normalizeCallerKeySecret({ secret: `nbk_${'A'.repeat(43)}`, metadata }, '0').secret,
    ).toHaveLength(47);
    expect(() =>
      normalizeCallerKeySecret({ secret: `nbk_${'A'.repeat(42)}B`, metadata }, '0'),
    ).toThrow(/CallerKey secret/);
    expect(() => normalizeCallerKeyAuthority(metadata, '2')).toThrow(/generation projection/i);
  });

  it('requires a complete, contiguous binding set', () => {
    const binding = (id: string, ord: number) => ({
      id,
      endpoint_key_id: '21',
      endpoint_base_url: 'https://example.com/v1',
      connector_type: 'openai-compatible',
      endpoint_note: '',
      endpoint_key_display_head: 'head',
      endpoint_key_display_tail: 'tail',
      endpoint_key_note: '',
      upstream_model_id: `model-${id}`,
      ord,
    });
    expect(
      normalizeBindingsResponse({
        bindings: [binding('1', 0), binding('2', 1)],
        binding_revision: '2',
      }).bindings,
    ).toHaveLength(2);
    expect(() =>
      normalizeBindingsResponse({
        bindings: [binding('1', 0), binding('2', 2)],
        binding_revision: '2',
      }),
    ).toThrow(/binding order/i);
  });

  it('preserves exact manual text while rejecting edge whitespace and oversized secrets', () => {
    expect(validateManualValue('Vendor/Exact', 512, false)).toBe('Vendor/Exact');
    expect(validateManualValue('界'.repeat(512), 512, false)).toHaveLength(512);
    expect(() => validateManualValue('界'.repeat(513), 512, false)).toThrow(
      /manual catalog value/i,
    );
    expect(() => validateManualValue(' Vendor/Exact', 512, false)).toThrow(/manual catalog value/i);
    expect(validateEndpointSecret('sk-valid')).toBe('sk-valid');
    expect(() => validateEndpointSecret('界'.repeat(22_000))).toThrow(/endpoint key secret/i);
  });

  it('counts astral input by Unicode scalar rather than UTF-16 code units', () => {
    expect(validateLogicalName('🧭'.repeat(64))).toBe('🧭'.repeat(64));
    expect(validatePersonalProviderName('😀'.repeat(64))).toBe('😀'.repeat(64));
    expect(validateManualValue('🛰️'.repeat(256), 512, false)).toBe('🛰️'.repeat(256));
    expect(validateManualValue('😀'.repeat(512), 512, false)).toBe('😀'.repeat(512));
    expect(() => validateLogicalName('😀'.repeat(65))).toThrow(/logical model name/i);
    expect(() => validateManualValue('😀'.repeat(513), 512, false)).toThrow(
      /manual catalog value/i,
    );
  });

  it('canonicalizes candidate filters so semantically identical requests share identity', () => {
    expect(canonicalCandidateFilters({ endpointId: '11', keyId: '21', source: 'manual' })).toEqual({
      endpointId: '11',
      keyId: '21',
      source: 'manual',
      query: '',
      cursor: '',
      limit: 50,
    });
    expect(
      canonicalCandidateFilters({ endpointId: '11', keyId: '21', source: 'manual', limit: 50 }),
    ).toEqual(canonicalCandidateFilters({ endpointId: '11', keyId: '21', source: 'manual' }));
    expect(() => canonicalCandidateFilters({ endpointId: '0' })).toThrow(/endpoint id/i);
    expect(() => canonicalCandidateFilters({ query: 'bad\u0085query' })).toThrow(
      /candidate query/i,
    );
  });
});
