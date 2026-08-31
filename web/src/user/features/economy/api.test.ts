import { describe, expect, it, vi } from 'vitest';
import { ApiError } from '@shared/query/http';
import { createDonation, contributeThursday, getDonations, isResponseUnknown } from './api';

const DONATION_RESPONSE = {
  id: '41',
  status: 'pending',
  revision: '1',
  description: 'Existing resources only',
  review_result: null,
  expires_at: null,
  keys: [
    {
      id: '51',
      endpoint_key_id: '61',
      display_head: 'sk-head',
      display_tail: 'tail',
      safe_source: {
        base_url: 'https://api.example.test/v1',
        connector_type: 'openai-compatible',
      },
      physical_enabled: false,
      charity_state: 'pending',
      limits: { price: null, calls: null, tokens: null },
      usage: {
        price_used: '0',
        price_inflight: '0',
        calls_used: '0',
        calls_inflight: '0',
        tokens_used: '0',
        tokens_inflight: '0',
      },
      token_reserve: 0,
      streak: { generation: '1', count: '0', failure_disabled: false },
      ended_reason: null,
    },
  ],
  created_at: 1_788_100_000,
  updated_at: 1_788_100_000,
};

function jsonResponse(value: unknown): Response {
  return new Response(JSON.stringify(value), {
    status: 200,
    headers: { 'content-type': 'application/json' },
  });
}

describe('economy mutation boundary', () => {
  it('distinguishes an explicit server rejection from a genuinely unknown response', () => {
    expect(isResponseUnknown(new ApiError('service_unavailable', 'try later', 503))).toBe(false);
    expect(isResponseUnknown(new ApiError('network_error', 'response lost', 0))).toBe(true);
    expect(isResponseUnknown(new ApiError('invalid_response', 'malformed 2xx', 200))).toBe(true);
  });

  it('submits only existing EndpointKey ids and explicit ownership authorization', async () => {
    vi.stubGlobal('crypto', { randomUUID: () => '12345678-1234-4234-8234-123456789abc' });
    const fetchMock = vi.fn<typeof fetch>(() => Promise.resolve(jsonResponse(DONATION_RESPONSE)));
    vi.stubGlobal('fetch', fetchMock);
    await createDonation({
      description: 'Existing resources only',
      endpointKeyIds: ['61', '62'],
      ownershipAuthorized: true,
    });
    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [path, init] = fetchMock.mock.calls[0];
    expect(path).toBe('/api/donations');
    expect(init?.method).toBe('POST');
    expect(JSON.parse(String(init?.body))).toEqual({
      description: 'Existing resources only',
      endpoint_key_ids: ['61', '62'],
      ownership_authorized: true,
    });
    expect(String(init?.body)).not.toMatch(/secret|base_url|connector_type/i);
    expect(new Headers(init?.headers).get('Idempotency-Key')).toBe(
      '12345678123442348234123456789abc',
    );
  });

  it('never retries a response-unknown mutation inside the request layer', async () => {
    const fetchMock = vi.fn<typeof fetch>(() => Promise.reject(new TypeError('network lost')));
    vi.stubGlobal('fetch', fetchMock);
    await expect(
      createDonation({
        description: 'One intent',
        endpointKeyIds: ['61'],
        ownershipAuthorized: true,
      }),
    ).rejects.toMatchObject({ code: 'network_error', status: 0 });
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it('rejects duplicate intent ids and duplicate paged identities before they can be trusted', async () => {
    const fetchMock = vi.fn<typeof fetch>(() =>
      Promise.resolve(
        jsonResponse({ data: [DONATION_RESPONSE, DONATION_RESPONSE], next_cursor: null }),
      ),
    );
    vi.stubGlobal('fetch', fetchMock);
    await expect(
      createDonation({
        description: 'Duplicate intent',
        endpointKeyIds: ['61', '61'],
        ownershipAuthorized: true,
      }),
    ).rejects.toMatchObject({ code: 'invalid_request', status: 400 });
    expect(fetchMock).not.toHaveBeenCalled();
    await expect(getDonations()).rejects.toMatchObject({ code: 'invalid_response', status: 200 });
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it('sends one fixed Thursday contribution without a client-side quantity', async () => {
    const fetchMock = vi.fn<typeof fetch>(() =>
      Promise.resolve(
        jsonResponse({
          count: '2',
          balance: '100.001',
          pool_balance: '250.002',
        }),
      ),
    );
    vi.stubGlobal('fetch', fetchMock);
    await contributeThursday('thu_abcdefghijklmnopqrstuA', '7');
    const [, init] = fetchMock.mock.calls[0];
    expect(JSON.parse(String(init?.body))).toEqual({
      period_id: 'thu_abcdefghijklmnopqrstuA',
      expected_revision: '7',
    });
    expect(String(init?.body)).not.toMatch(/quantity|remaining|times/i);
  });
});
