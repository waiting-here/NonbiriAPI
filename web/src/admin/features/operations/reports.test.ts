import { describe, expect, it } from 'vitest';
import { normalizeReportBadge, normalizeReportDecisionReceipt, normalizeReportDetail, normalizeReportSummary } from './reports';

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
});
