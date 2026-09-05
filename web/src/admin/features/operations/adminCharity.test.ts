import { describe, expect, it, vi } from 'vitest';
import {
  getAdminCharityDonations,
  groupAdminCharityDonations,
  normalizeAdminCharityDonation,
  type AdminCharityDonation,
} from './adminCharity';

const channelID = `mch_${'A'.repeat(22)}`;

function key(
  source: Record<string, unknown>,
  id: string,
  overrides: Record<string, unknown> = {},
): Record<string, unknown> {
  return {
    id,
    endpoint_key_id: '91',
    display_head: 'head',
    display_tail: 'tail',
    safe_source: source,
    physical_enabled: true,
    charity_state: 'available',
    limits: { price: '1.001', calls: '10', tokens: null },
    usage: {
      price_used: '0',
      price_inflight: '0',
      calls_used: '1',
      calls_inflight: '0',
      tokens_used: '0',
      tokens_inflight: '0',
    },
    token_reserve: 0,
    authorized_expires_at: 2_000,
    expires_at: 1_000,
    streak: { generation: '1', count: '0', failure_disabled: false },
    ended_reason: null,
    safe_note: 'reviewer-only text must not cross the feature boundary',
    ...overrides,
  };
}

function donationWire(
  id: string,
  keys: Record<string, unknown>[],
  overrides: Record<string, unknown> = {},
): Record<string, unknown> {
  return {
    id,
    status: 'approved',
    revision: '2',
    description: 'donor private description must not render',
    review_result: {
      decision: 'approve',
      reason: 'reviewer-private reason must not render',
      reviewed_at: 2,
    },
    keys,
    owner: { user_id: '7', discord_id: 'discord-private', display_name: 'Donor private name' },
    reviewer: { user_id: '8', role: 'admin' },
    created_at: 1,
    updated_at: 2,
    ...overrides,
  };
}

function donation(
  id: string,
  keys: Record<string, unknown>[],
  overrides: Record<string, unknown> = {},
): AdminCharityDonation {
  return normalizeAdminCharityDonation(donationWire(id, keys, overrides));
}

const mainstream = {
  kind: 'mainstream',
  channel_id: channelID,
  channel_revision: '3',
  connector_type: 'openai-compatible',
  base_url: 'https://api.example.test/v1',
  name: 'Example subscription',
  category: 'subscription',
};

const custom = {
  kind: 'custom',
  connector_type: 'openai-compatible',
  base_url: 'https://api.example.test/v1',
};

function jsonResponse(value: unknown): Response {
  return new Response(JSON.stringify(value), {
    status: 200,
    headers: { 'content-type': 'application/json' },
  });
}

describe('administrator charity grouped projection', () => {
  it('merges only the exact mainstream provenance tuple and keeps custom keys independent', () => {
    const values = [
      donation('1', [key(mainstream, '11')]),
      donation('2', [key(mainstream, '12')]),
      donation('3', [key({ ...mainstream, channel_revision: '4' }, '13')]),
      donation('4', [key(custom, '14')]),
      donation('5', [key(custom, '15')]),
    ];
    const groups = groupAdminCharityDonations(values);
    expect(groups).toHaveLength(4);
    expect(groups.find((group) => group.source.kind === 'mainstream' && group.source.channel_revision === '3')?.items.map((item) => item.key.id)).toEqual(['11', '12']);
    expect(groups.filter((group) => group.source.kind === 'custom')).toHaveLength(2);
  });

  it('does not retain endpoint identity, safe notes, or donor/reviewer text', () => {
    const result = donation('1', [key(mainstream, '11')]);
    expect(JSON.stringify(result)).not.toContain('endpoint_key_id');
    expect(JSON.stringify(result)).not.toContain('safe_note');
    expect(JSON.stringify(result)).not.toContain('donor private');
    expect(JSON.stringify(result)).not.toContain('reviewer-private');
    expect(result.keys[0]).not.toHaveProperty('authorized_endpoint_key_id');
  });

  it('accepts deidentified nullable identities and a removed physical key', () => {
    const result = donation(
      '1',
      [key(custom, '11', { endpoint_key_id: null })],
      {
        owner: { user_id: '7', discord_id: null, display_name: 'Donor' },
        reviewer: { user_id: null, role: 'steward' },
      },
    );
    expect(result.keys[0].id).toBe('11');
  });

  it('accepts the empty system review used by automatic mainstream approval', () => {
    const result = donation(
      '1',
      [key(mainstream, '11')],
      {
        review_result: { decision: 'approve', reason: '', reviewed_at: 2 },
        reviewer: null,
      },
    );
    expect(result.status).toBe('approved');
  });

  it.each(['admin', 'steward', 'level5'])('accepts a manual %s review without retaining its identity', (role) => {
    const result = donation('1', [key(custom, '11')], {
      review_result: { decision: 'approve', reason: '', reviewed_at: 2 },
      reviewer: { user_id: '8', role },
    });
    expect(result.status).toBe('approved');
    expect(result).not.toHaveProperty('reviewer');
    expect(result).not.toHaveProperty('review_result');
  });

  it('accepts a clock-expired key while lifecycle cleanup is pending', () => {
    const result = donation('1', [
      key(mainstream, '11', { charity_state: 'expired', ended_reason: null }),
    ]);
    expect(result.keys[0]).toMatchObject({ charity_state: 'expired', ended_reason: null });
  });

  it('accepts sixteen ASCII display bytes and rejects Unicode fragments', () => {
    const result = donation('1', [
      key(custom, '11', {
        display_head: 'A'.repeat(16),
        display_tail: 'Z'.repeat(16),
      }),
    ]);
    expect(result.keys[0]).toMatchObject({
      display_head: 'A'.repeat(16),
      display_tail: 'Z'.repeat(16),
    });
    expect(() => donation('1', [key(custom, '11', { display_head: 'é' })])).toThrow(/key head/i);
    expect(() => donation('1', [key(custom, '11', { display_tail: 'é' })])).toThrow(/key tail/i);
  });

  it('accepts permanent authorization with either effective expiry and bounds finite authorization', () => {
    const finiteEffective = donation('1', [
      key(mainstream, '11', { authorized_expires_at: null, expires_at: 1_000 }),
    ]);
    expect(finiteEffective.keys[0]).toMatchObject({
      authorized_expires_at: null,
      expires_at: 1_000,
    });
    const permanentEffective = donation('2', [
      key(mainstream, '12', { authorized_expires_at: null, expires_at: null }),
    ]);
    expect(permanentEffective.keys[0]).toMatchObject({
      authorized_expires_at: null,
      expires_at: null,
    });
    expect(() => donation('3', [key(mainstream, '13', { expires_at: null })])).toThrow(
      /expiry bounds/i,
    );
    expect(() => donation('4', [key(mainstream, '14', { expires_at: 2_001 })])).toThrow(
      /expiry bounds/i,
    );
  });

  it('fails closed on malformed provenance and invalid expiry bounds', () => {
    expect(() => donation('1', [key({ ...mainstream, extra: 'not-allowed' }, '11')])).toThrow(
      /mainstream charity source/i,
    );
    expect(() => donation('1', [key(mainstream, '11', { ended_reason: 'physical_deleted' })])).toThrow(
      /ended reason/i,
    );
    expect(() => donation('1', [key({ ...mainstream, name: 'bad\u0080name' }, '11')])).toThrow(
      /channel name/i,
    );
    expect(() => donation('1', [key({ ...custom, base_url: 'https://bad\u0080.example' }, '11')])).toThrow(
      /source URL/i,
    );
    expect(() => donation('1', [key(mainstream, '11', { charity_state: 'available', ended_reason: 'expired' })])).toThrow(
      /ended reason/i,
    );
    expect(() => donation('1', [key(mainstream, '11', { charity_state: 'ended', ended_reason: null })])).toThrow(
      /ended reason/i,
    );
  });

  it('reads the closed admin page with a status filter and cursor', async () => {
    const fetchMock = vi.fn<typeof fetch>(async (input) => {
      expect(String(input)).toBe('/admin/api/donations?status=approved&cursor=YWJj&limit=50');
      return jsonResponse({ data: [donationWire('1', [key(custom, '11')])], next_cursor: null });
    });
    vi.stubGlobal('fetch', fetchMock);
    await expect(getAdminCharityDonations('approved', 'YWJj')).resolves.toMatchObject({
      next_cursor: null,
      data: [{ id: '1', keys: [{ id: '11' }] }],
    });
    expect(() => getAdminCharityDonations('unknown')).toThrow(/status filter/i);
    expect(() => getAdminCharityDonations('', 'bad=')).toThrow(/cursor/i);
  });
});
