import { describe, expect, it, vi } from 'vitest';
import {
  getReportTargetDonations,
  normalizeReportBadge,
  normalizeReportDecisionReceipt,
  normalizeReportDetail,
  normalizeReportDonationMatch,
  normalizeReportSummary,
  normalizeReportTarget,
} from './reports';

const expired = {
  id: `rpc_${'A'.repeat(22)}`,
  status: 'expired',
  connector_type: 'openai-compatible',
  canonical_base_url: 'https://api.example.com/v1',
  material_version: '1',
  target_version: '1',
  deadline: 2,
  counts: { materials: '1', targets: '1', distinct_owners: '1', processed: '0', deleted: '0', released: '0' },
  retry: null,
  created_at: 1,
  terminal_at: 2,
};

const target = {
  id: `rpt_${'A'.repeat(22)}`,
  target_seq: '1',
  state: 'released',
  endpoint_key_id: null,
  key_ref: 'A'.repeat(43),
  owner: null,
  endpoint: {
    connector_type: 'openai-compatible',
    canonical_base_url: 'https://api.example.com/v1',
    display_head: 'head',
    display_tail: 'tail',
  },
  discovered_version: '2',
  decided_version: '3',
  donation_match_count: '2',
  created_at: 1,
  updated_at: 2,
};

const lineage = {
  donation_id: '7',
  donation_key_id: '8',
  donation_status: 'deleted',
  key_state: 'ended',
  expires_at: 10,
  ended_reason: 'account_deleted',
  ended_at: 11,
};

describe('administrator report wire', () => {
  it('accepts both frozen expired progress terminals', () => {
    expect(normalizeReportSummary({ ...expired, progress_state: 'in_progress' }).progress_state).toBe('in_progress');
    expect(normalizeReportSummary({ ...expired, progress_state: 'complete' }).progress_state).toBe('complete');
  });

  it('requires the badge total to equal its three actionable states', () => {
    expect(normalizeReportBadge({ total: '6', by_status: { pending_indexing: '1', pending_review: '2', approved_processing: '3' } }).total).toBe('6');
    expect(() => normalizeReportBadge({ total: '7', by_status: { pending_indexing: '1', pending_review: '2', approved_processing: '3' } })).toThrow(/badge total/i);
  });

  it('strictly validates the decision receipt expected for each mutation', () => {
    const receipt = {
      id: `rpc_${'A'.repeat(22)}`,
      status: 'approved_processing',
      material_version: '3',
      target_version: '4',
    };
    expect(normalizeReportDecisionReceipt(receipt, 'approved_processing')).toEqual(receipt);
    expect(() => normalizeReportDecisionReceipt(receipt, 'rejected')).toThrow(/receipt status/i);
    expect(() => normalizeReportDecisionReceipt({ ...receipt, extra: true }, 'approved_processing')).toThrow();
  });

  it('strictly decodes the recorded decision and its nested material cursor', () => {
    const detail = {
      ...expired,
      progress_state: 'complete',
      materials: { data: [], next_cursor: 'YWJj' },
      decision: { action: 'expire', reason: 'deadline elapsed', actor_user_id: null, created_at: 2 },
    };
    expect(normalizeReportDetail(detail).decision).toEqual(detail.decision);
    expect(() => normalizeReportDetail({ ...detail, materials: { data: [], next_cursor: 'abc=' } })).toThrow(/cursor/i);
  });

  it('decodes donation match counts and the closed lineage item', () => {
    expect(normalizeReportTarget(target).donation_match_count).toBe('2');
    expect(normalizeReportDonationMatch(lineage)).toEqual(lineage);
    expect(() => normalizeReportTarget({ ...target, donation_match_count: '02' })).toThrow(/count/i);
    expect(() => normalizeReportDonationMatch({ ...lineage, safe_note: 'not in lineage wire' })).toThrow(
      /report donation match/i,
    );
    expect(() => normalizeReportDonationMatch({ ...lineage, ended_reason: 'physical_deleted' })).toThrow(
      /ended reason/i,
    );
  });

  it('requests lineage through the case/target route with bounded cursor pagination', async () => {
    const fetchMock = vi.fn<typeof fetch>(async (input) => {
      expect(String(input)).toBe(
        `/admin/api/reports/${expired.id}/targets/${target.id}/donations?cursor=YWJj&limit=50`,
      );
      return new Response(JSON.stringify({ data: [lineage], next_cursor: null }), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      });
    });
    vi.stubGlobal('fetch', fetchMock);
    await expect(getReportTargetDonations(expired.id, target.id, 'YWJj')).resolves.toEqual({
      data: [lineage],
      next_cursor: null,
    });
  });
});
