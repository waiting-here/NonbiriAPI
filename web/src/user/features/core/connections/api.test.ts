import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it, vi } from 'vitest';
import { addBindings, createEndpointKey, patchEndpointKey } from '../api';
import { normalizeDiscoveryModel, normalizeDiscoverySource } from './api';
import { executeConnection, readConnectionResult } from './resourceOperation';
import { normalizeRerankFormat } from '@shared/connections';
const identity = { idempotencyKey: 'ABCDEFGHIJKLMNOPQRSTUV', actionId: 'connection-test' };
const credential = {
  id: '41',
  connection_id: '2',
  note: 'shared',
  enabled: true,
  revision: '3',
  credential_version: '2',
  display_head: 'sk-a',
  display_tail: 'last',
  created_at: 1700000000,
  updated_at: 1700000001,
};
const source = {
  id: '51',
  connection_id: '2',
  name: 'Catalog',
  base_url: 'https://catalog.example/root',
  models_url: 'https://catalog.example/root/model-list',
  list_format: 'openai',
  credential_id: '41',
  credential_version: '2',
  revision: '3',
  discovery_source_revision: '2',
  enabled: true,
  state: 'failed',
  checked_at: 1700000000,
  safe_class: 'timeout',
  safe_diag: 'Timed out',
  created_at: 1700000000,
  updated_at: 1700000001,
};
const response = (value: unknown, status = 200) =>
  new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } });
function keyFixture() {
  return JSON.parse(
    readFileSync(
      resolve(process.cwd(), '../internal/resources/testdata/endpoint_key.json'),
      'utf8',
    ),
  ) as Record<string, unknown>;
}
describe('connection API boundaries', () => {
  it('wraps an explicit one-target service choice and reads the full projection after a compact receipt', async () => {
    const bindings = JSON.parse(
      readFileSync(resolve(process.cwd(), '../internal/resources/testdata/bindings.json'), 'utf8'),
    );
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(
        response(
          { model_id: '31', binding_revision: '6', added_count: '1', binding_count: '1' },
          201,
        ),
      )
      .mockResolvedValue(response(bindings));
    vi.stubGlobal('fetch', fetchMock);
    await addBindings(
      '31',
      '5',
      [
        {
          endpoint_key_id: '21',
          upstream_model_id: 'Vendor/Exact',
          native_services: ['responses'],
        },
      ],
      identity,
    );
    expect(String(fetchMock.mock.calls[0]?.[0])).toContain('/api/models/31/bindings/batch-receipt');
    expect(JSON.parse(String(fetchMock.mock.calls[0]?.[1]?.body))).toEqual({
      expected_binding_revision: '5',
      selections: [
        {
          targets: [
            {
              endpoint_key_id: '21',
              upstream_model_id: 'Vendor/Exact',
              native_services: ['responses'],
            },
          ],
        },
      ],
    });
    expect(String(fetchMock.mock.calls[1]?.[0])).toContain('/api/models/31/bindings');
  });

  it('keeps backend catalog URL and unknown service evidence without inferring capabilities', () => {
    expect(normalizeDiscoverySource(source).models_url).toBe(source.models_url);
    expect(
      normalizeDiscoveryModel({
        id: '1',
        upstream_model_id: 'gpt-possible-name',
        provider: '',
        native_services: [],
        service_evidence: 'unknown',
        needs_service_selection: true,
      }),
    ).toMatchObject({ native_services: [], needs_service_selection: true });
    expect(normalizeRerankFormat('')).toBe('');
    expect(() => normalizeRerankFormat(null)).toThrow();
  });
  it('grants an existing credential without a secret and explicitly detaches discovery when changing credentials', async () => {
    const key = {
      ...keyFixture(),
      credential_id: '41',
      credential_version: '2',
      grant_revision: '4',
      native_services: ['chat_completions', 'responses'],
      discovery_source_id: null,
    };
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(response(key, 201))
      .mockImplementation(async () => response(key));
    vi.stubGlobal('fetch', fetchMock);
    await createEndpointKey(
      '11',
      {
        credential_id: '41',
        native_services: ['chat_completions', 'responses'],
        discovery_source_id: null,
        note: 'shared',
        enabled: true,
        force_store_false: false,
        ownership_confirmed: true,
      },
      identity,
    );
    await patchEndpointKey(
      '11',
      '21',
      {
        credential_id: '41',
        native_services: ['responses'],
        discovery_source_id: null,
        expected_revision: '1',
      },
      identity,
    );
    const bodies = fetchMock.mock.calls.map(
      ([, init]) => JSON.parse(String(init?.body)) as Record<string, unknown>,
    );
    expect(bodies[0]).toMatchObject({
      credential_id: '41',
      native_services: ['chat_completions', 'responses'],
      discovery_source_id: null,
    });
    expect(bodies[0]).not.toHaveProperty('secret');
    expect(bodies[1]).toMatchObject({
      credential_id: '41',
      native_services: ['responses'],
      discovery_source_id: null,
    });
  });
  it('rereads safe current metadata after rotation and receipt recovery', async () => {
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(response({ credential_id: '41', credential_version: '2' }, 201))
      .mockImplementation(async () => response(credential));
    vi.stubGlobal('fetch', fetchMock);
    const intent = {
      kind: 'credential_version' as const,
      credentialId: '41',
      expectedRevision: '2',
    };
    await expect(
      executeConnection(
        intent,
        'synthetic-secret-value',
        identity.idempotencyKey,
        new AbortController().signal,
      ),
    ).resolves.toEqual({ kind: 'credential_version', credential });
    expect(JSON.parse(String(fetchMock.mock.calls[0]?.[1]?.body))).toEqual({
      secret: 'synthetic-secret-value',
      ownership_confirmed: true,
      expected_revision: '2',
    });
    await expect(
      readConnectionResult(
        intent,
        {
          status: 'recorded',
          stage: 'credential_version',
          result: { credential_id: '41', credential_version: '1' },
        },
        new AbortController().signal,
      ),
    ).resolves.toEqual({ kind: 'credential_version', credential });
    await expect(
      readConnectionResult(
        intent,
        { status: 'recorded', stage: 'credential_version', result: { credential_id: '42' } },
        new AbortController().signal,
      ),
    ).rejects.toMatchObject({ code: 'invalid_response' });
  });
});
