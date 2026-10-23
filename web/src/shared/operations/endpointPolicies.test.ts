import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  getCloudflareRoutes,
  getEndpointPolicies,
  getEndpointPolicy,
  getEndpointPolicyOptions,
  normalizeCloudflareRoutes,
  normalizeEndpointPolicy,
  saveEndpointPolicy,
  type EndpointBackoffPolicy,
} from './endpointPolicies';

const endpoint = { connector_type: 'openai-compatible', base_url: 'https://provider.example/v1' };
const policy: EndpointBackoffPolicy = {
  ...endpoint,
  revision: '2',
  rules: [{ kind: 'code', value: 'insufficient_credits', period: 'auto' }],
};
const route = {
  source_base_url: endpoint.base_url,
  gateway_origin: 'https://gateway.example',
  credential_header: 'X-Api-Key',
  credential_query_parameter: 'key',
  retain_credential_header: true,
};
const json = (value: unknown) =>
  new Response(JSON.stringify(value), { headers: { 'content-type': 'application/json' } });
afterEach(() => vi.unstubAllGlobals());

describe('endpoint policy wire contracts', () => {
  it('decodes ordered rules and rejects invalid periods, oversized rules and numeric revisions', () => {
    expect(normalizeEndpointPolicy(policy)).toEqual(policy);
    expect(normalizeEndpointPolicy({ ...policy, revision: '0', rules: [] }).rules).toEqual([]);
    for (const value of [
      { ...policy, revision: 2 },
      { ...policy, rules: Array.from({ length: 9 }, () => policy.rules[0]) },
      { ...policy, rules: [{ ...policy.rules[0], period: 'never' }] },
      { ...policy, rules: [{ ...policy.rules[0], kind: 'regex' }] },
    ])
      expect(() => normalizeEndpointPolicy(value)).toThrow();
  });
  it('projects only safe loaded route fields and rejects a full gateway path', () => {
    expect(
      normalizeCloudflareRoutes({ enabled: false, mode: 'direct', worker_origin: '', routes: [] }),
    ).toEqual({
      enabled: false,
      mode: 'direct',
      worker_origin: '',
      routes: [],
    });
    expect(
      normalizeCloudflareRoutes({
        enabled: true,
        mode: 'legacy',
        worker_origin: '',
        routes: [{ ...route, token: 'not-for-display', token_env: 'PRIVATE_TOKEN' }],
      }),
    ).toEqual({ enabled: true, mode: 'legacy', worker_origin: '', routes: [route] });
    expect(() =>
      normalizeCloudflareRoutes({
        enabled: true,
        mode: 'legacy',
        worker_origin: '',
        routes: [{ ...route, gateway_origin: 'https://gateway.example/private/account/path' }],
      }),
    ).toThrow();
    expect(() =>
      normalizeCloudflareRoutes({
        enabled: true,
        mode: 'legacy',
        worker_origin: '',
        routes: Array.from({ length: 33 }, () => route),
      }),
    ).toThrow();
    expect(() =>
      normalizeCloudflareRoutes({
        enabled: true,
        mode: 'legacy',
        worker_origin: '',
        routes: [{ ...route, retain_credential_header: 'true' }],
      }),
    ).toThrow();
  });
  it('projects only a safe Worker origin and validates the actual global mode', () => {
    const status = {
      enabled: true,
      mode: 'worker',
      worker_origin: 'https://worker.example',
      routes: [route],
    };
    expect(
      normalizeCloudflareRoutes({
        ...status,
        worker_url: 'https://worker.example/private',
        token: 'private-token',
      }),
    ).toEqual(status);
    for (const patch of [
      { mode: 'unknown' },
      { worker_origin: 'https://worker.example/private' },
      { worker_origin: 'https://user:pass@worker.example' },
      { worker_origin: '' },
      { mode: 'legacy' },
    ])
      expect(() => normalizeCloudflareRoutes({ ...status, ...patch })).toThrow();
  });
  it('uses role-specific endpoints, numbered windows and unchanged idempotent input', async () => {
    const requests: { path: string; method?: string; body: unknown; key: string | null }[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn<typeof fetch>(async (input, init) => {
        const path = String(input);
        requests.push({
          path,
          method: init?.method,
          body: init?.body ? JSON.parse(String(init.body)) : null,
          key: new Headers(init?.headers).get('Idempotency-Key'),
        });
        if (path.includes('/endpoint-backoff-policies'))
          return json({
            data: [{ ...endpoint, revision: '2', rule_count: '1', key_count: '23' }],
            next_cursor: null,
            pagination: { page: '2', page_size: 10, total_items: '11', total_pages: '2' },
          });
        if (path.endsWith('/endpoint-backoff-options'))
          return json({ connector_types: ['openai-compatible', 'google-gemini'] });
        if (path.endsWith('/egress/cloudflare-routes'))
          return json({ enabled: true, mode: 'legacy', worker_origin: '', routes: [route] });
        return json(policy);
      }),
    );
    const page = await getEndpointPolicies('steward', 'provider', '2', 10);
    expect(page.data[0]?.key_count).toBe('23');
    await getEndpointPolicy('admin', policy);
    expect(await getEndpointPolicyOptions('admin')).toEqual(['openai-compatible', 'google-gemini']);
    await getCloudflareRoutes('steward');
    const input = { ...endpoint, expected_revision: '2', rules: policy.rules };
    await saveEndpointPolicy('admin', input, 'original-request');
    await saveEndpointPolicy('admin', input, 'original-request');
    expect(requests[0]?.path).toBe(
      '/api/steward/endpoint-backoff-policies?q=provider&page=2&page_size=10',
    );
    expect(new URL(requests[1]!.path, 'https://example.test').searchParams.get('base_url')).toBe(
      endpoint.base_url,
    );
    expect([...new URL(requests[1]!.path, 'https://example.test').searchParams.keys()]).toEqual([
      'connector_type',
      'base_url',
    ]);
    expect(requests[2]?.path).toBe('/admin/api/endpoint-backoff-options');
    expect(requests[3]).toMatchObject({
      path: '/api/steward/egress/cloudflare-routes',
      body: null,
    });
    expect(requests[4]).toEqual({
      path: '/admin/api/endpoint-backoff-policy',
      method: 'PUT',
      body: input,
      key: 'original-request',
    });
    expect(requests[5]).toEqual(requests[4]);
  });
  it('rejects a saved policy for a different endpoint so its outcome remains unknown', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn<typeof fetch>(async () => json({ ...policy, base_url: 'https://other.example/v1' })),
    );
    await expect(
      saveEndpointPolicy('admin', { ...endpoint, expected_revision: '2', rules: [] }, 'same'),
    ).rejects.toMatchObject({ code: 'invalid_response' });
  });
});
