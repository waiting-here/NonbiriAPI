import { ApiError } from '@shared/query/http';
import { describe, expect, it, vi } from 'vitest';
import {
  REPORT_ACCEPTED_MESSAGE,
  announcementDismissalKey,
  normalizeIssue,
  normalizeIssuePage,
  normalizeUserAuthority,
  shouldRetainCredentialReportIntent,
  submitCredentialReport,
} from './data';

describe('user operations contracts', () => {
  const issue = {
    id: `iss_${'A'.repeat(22)}`, state: 'current', safe_detail: '', deep_link: null,
    first_seen_at: 1, last_seen_at: 2, count: '1', closed_at: null,
  };

  it.each([
    ['model_discovery', 'endpoint_key', 'discovery_failed'],
    ['routing_projection', 'model', 'no_routable_binding'],
    ['resource_validator', 'endpoint', 'credential_invalid'],
    ['resource_validator', 'endpoint', 'configuration_invalid'],
    ['resource_validator', 'endpoint_key', 'credential_invalid'],
    ['resource_validator', 'endpoint_key', 'configuration_invalid'],
  ] as const)('accepts the frozen issue tuple %s/%s/%s', (source, resource_kind, summary_code) => {
    expect(normalizeIssue({ ...issue, source, resource_kind, summary_code })).toMatchObject({ source, resource_kind, summary_code });
  });

  it('rejects crossed issue tuples and incomplete or mismatched deep links', () => {
    const discovery = { ...issue, source: 'model_discovery', resource_kind: 'endpoint_key', summary_code: 'discovery_failed' };
    expect(() => normalizeIssue({ ...discovery, resource_kind: 'model' })).toThrow(/tuple/i);
    expect(() => normalizeIssue({ ...discovery, deep_link: { route_id: 'endpoint-detail' } })).toThrow(/deep link/i);
    expect(() => normalizeIssue({ ...discovery, deep_link: { route_id: 'models', resource_id: '7' } })).toThrow(/deep link route/i);
  });

  it('fails closed when an issue page carries a non-canonical cursor', () => {
    expect(() => normalizeIssuePage({ data: [], next_cursor: 'abc=', projection_incomplete: false })).toThrow(/cursor/i);
  });

  it('accepts only the equalized no-store report receipt', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ accepted: true, message: REPORT_ACCEPTED_MESSAGE }), {
      status: 202,
      headers: { 'Content-Type': 'application/json', 'Cache-Control': 'no-store', 'X-Nonbiri-Report-Accepted': '1' },
    }));
    vi.stubGlobal('fetch', fetchMock);
    const secret = 'sk-report-only-in-request-memory';
    await expect(submitCredentialReport({ connector_type: 'openai-compatible', base_url: 'https://api.example.com/v1', secret, note: '' }, 'idem-1')).resolves.toBeUndefined();
    const [path, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(path).toBe('/api/reports/credential-theft');
    expect(init.cache).toBe('no-store');
    expect(init.credentials).toBe('same-origin');
    expect(init.headers).toMatchObject({ 'Cache-Control': 'no-store', 'Idempotency-Key': 'idem-1' });
    expect(JSON.parse(String(init.body))).toMatchObject({ secret });
  });

  it('rejects non-equalized receipts without echoing the submitted secret', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({ accepted: true, message: 'matched' }), {
      status: 202,
      headers: { 'Content-Type': 'application/json', 'Cache-Control': 'no-store', 'X-Nonbiri-Report-Accepted': '1' },
    })));
    const secret = 'sk-must-not-appear-in-errors';
    await expect(submitCredentialReport({ connector_type: 'openai-compatible', base_url: 'https://api.example.com/v1', secret, note: '' }, 'idem-2'))
      .rejects.toSatisfy((error: unknown) => error instanceof Error && !error.message.includes(secret));
  });

  it('scopes dismissal state by epoch, account, announcement, and revision', () => {
    expect(announcementDismissalKey('7', { epoch: '3', id: `ann_${'A'.repeat(22)}`, revision: '5' }))
      .toBe(`nonbiri:announcement-dismissed:3:7:ann_${'A'.repeat(22)}:5`);
  });

  it('accepts a canonical fractional balance in the Generation 2 session projection', () => {
    expect(normalizeUserAuthority({ user: {
      id: '7', username: 'user', avatar: null, avatar_url: null, guild_nick: null, guild_avatar_url: null,
      lang: 'en', is_banned: false, banned_until: null, charity_suspended_until: null,
      endpoint_limit: null, effective_endpoint_limit: '5', rpm_limit: null, effective_rpm_limit: '60',
      concurrency_limit: null, effective_concurrency_limit: '2', balance: '1.5', donation_credit: '0',
      effective_level: 1, level_display_name: 'Lv1', game_profile_public: false, created_at: 1, updated_at: 1,
      usage: {
        total_requests: '0', total_uncached_input_tokens: '0', total_cache_write_input_tokens: '0',
        total_cache_read_input_tokens: '0', total_output_tokens: '0', total_prompt_tokens: '0',
        total_completion_tokens: '0', total_unknown_usage_requests: '0',
      },
    } }).id).toBe('7');
  });

  it('retains the report key only when the response outcome is unknown', () => {
    expect(shouldRetainCredentialReportIntent(new ApiError('network_error', 'offline', 0))).toBe(true);
    expect(shouldRetainCredentialReportIntent(new ApiError('invalid_response', 'bad receipt', 202))).toBe(true);
    expect(shouldRetainCredentialReportIntent(new ApiError('http_error', 'server', 500))).toBe(true);
    expect(shouldRetainCredentialReportIntent(new ApiError('conflict', 'different intent', 409))).toBe(false);
    expect(shouldRetainCredentialReportIntent(new ApiError('http_error', 'rejected', 400))).toBe(false);
  });
});
