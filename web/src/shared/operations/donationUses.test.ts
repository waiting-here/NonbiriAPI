import { describe, expect, it, vi } from 'vitest';
import {
  normalizeDonationUse,
  reviewDonationUses,
  addDonationUses,
  getDonationUses,
} from './donationUses';
import { readDonationDiscovery, startDonationDiscovery } from './donationDiscovery';
const version = (number: string, status: string) => ({
  credential_id: '8',
  credential_version: number,
  grant_revision: number,
  status,
  review_note: 'Visible decision',
  reviewed_at: null,
  expected_review_revision: 'body-' + number,
  discovery_source_id: null,
  discovery_source_revision: null,
  display_head: 'head-' + number,
  display_tail: 'tail',
  review: { required: true, revision: 'body-' + number, material_available: true },
  discovery: null,
});
const use = {
  id: '6',
  donation_id: '3',
  donation_key_id: '4',
  endpoint_key_id: '5',
  native_service: 'responses',
  enabled: true,
  discovery_enabled: false,
  revision: '9',
  approved: version('1', 'approved'),
  pending: version('2', 'pending'),
  latest: version('2', 'pending'),
  health: {
    failure_streak: '2',
    failure_disabled: false,
    retry_after_until: null,
    backoff_retry_at: null,
  },
  capabilities: {
    can_review: true,
    can_manage: false,
    can_reset_failures: true,
    can_discover: false,
    can_bind: true,
    unavailable_reason: null,
  },
};
const response = (value: unknown) =>
  new Response(JSON.stringify(value), { headers: { 'Content-Type': 'application/json' } });
describe('versioned donation uses', () => {
  it('keeps approved and pending material distinct without borrowing a missing version', () => {
    const row = normalizeDonationUse(use);
    expect(row.approved?.display_head).toBe('head-1');
    expect(row.pending?.display_head).toBe('head-2');
    expect(row.approved?.expected_review_revision).toBe('body-1');
    expect(normalizeDonationUse({ ...use, approved: null }).approved).toBeNull();
    expect(row.capabilities.can_manage).toBe(false);
  });
  it('accepts a hidden or removed endpoint key without borrowing owner information', () => {
    const row = normalizeDonationUse({ ...use, endpoint_key_id: null, health: null });
    expect(row.endpoint_key_id).toBeNull();
    expect(row.pending?.display_head).toBe('head-2');
  });
  it('sends stable use CAS and the pending body revision and preserves partial batch failures', async () => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(
      response({
        results: [
          { use_id: '6', ok: true },
          { use_id: '7', ok: false, error: { code: 'conflict', message: 'Version changed' } },
        ],
      }),
    );
    vi.stubGlobal('fetch', fetchMock);
    const items = [
      {
        use_id: '6',
        expected_revision: '9',
        expected_review_revision: 'body-2',
        decision: 'approve' as const,
        review_note: 'Checked',
      },
    ];
    const result = await reviewDonationUses('admin', '3', items, 'ABCDEFGHIJKLMNOPQRSTUV');
    expect(JSON.parse(String(fetchMock.mock.calls[0]?.[1]?.body))).toEqual({ items });
    expect(result).toEqual([
      { use_id: '6', ok: true },
      { use_id: '7', ok: false, error: { code: 'conflict', message: 'Version changed' } },
    ]);
  });
  it('adds explicit services on the existing member with exact grant and use revisions and reads only that key', async () => {
    const input = {
      expected_revision: '2',
      donation_key_id: '4',
      credential_id: '8',
      credential_version: '3',
      grant_revision: '5',
      items: [
        { native_service: 'responses' as const, expected_revision: '9' },
        { native_service: 'embeddings' as const, expected_revision: null },
      ],
      discovery_source_id: null,
      discovery_source_revision: null,
    };
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(
        response({
          donation_revision: '3',
          uses: [
            { id: '6', revision: '10', native_service: 'responses' },
            { id: '7', revision: '1', native_service: 'embeddings' },
          ],
        }),
      )
      .mockResolvedValueOnce(
        response({
          data: [],
          next_cursor: null,
          pagination: { page: '1', page_size: 20, total_items: '0', total_pages: '1' },
        }),
      );
    vi.stubGlobal('fetch', fetchMock);
    await addDonationUses('3', input, 'ABCDEFGHIJKLMNOPQRSTUV');
    await getDonationUses('owner', '3', '1', 20, '', undefined, '4');
    expect(String(fetchMock.mock.calls[0]?.[0])).toBe('/api/donations/3/uses');
    expect(JSON.parse(String(fetchMock.mock.calls[0]?.[1]?.body))).toEqual(input);
    expect(String(fetchMock.mock.calls[1]?.[0])).toBe(
      '/api/donations/3/uses?page=1&page_size=20&donation_key_id=4',
    );
  });
  it('pins discovery refresh and polling to one approved use without changing charity model scope', async () => {
    const checking = {
      state: 'checking',
      revision: '4',
      result: null,
      safe_class: 'none',
      observed_at: 1700000000,
      count: null,
    };
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(response({ operation_id: 'op_' + 'A'.repeat(22), evidence: checking }))
      .mockResolvedValue(response(checking));
    vi.stubGlobal('fetch', fetchMock);
    const ref = { donation_id: '3', key_id: '4', use_id: '6', expected_revision: '9' };
    await startDonationDiscovery('steward', ref, 'ABCDEFGHIJKLMNOPQRSTUV', '20');
    await readDonationDiscovery('steward', ref, '20');
    expect(String(fetchMock.mock.calls[0]?.[0])).toBe(
      '/api/steward/donations/3/keys/4/models/refresh?charity_model_id=20',
    );
    expect(JSON.parse(String(fetchMock.mock.calls[0]?.[1]?.body))).toEqual({
      use_id: '6',
      expected_revision: '9',
    });
    expect(String(fetchMock.mock.calls[1]?.[0])).toBe(
      '/api/steward/donations/3/keys/4/models/discovery?use_id=6&expected_revision=9&charity_model_id=20',
    );
  });
});
