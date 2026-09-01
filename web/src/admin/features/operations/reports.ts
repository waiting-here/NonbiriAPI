import { decoded, idempotentOptions, queryPath } from '@shared/operations/api';
import {
  decimal, decimalID, invalidResponse, nullableDecimalID, nullableUnixSecond,
  oneOf, opaqueID, page, record, string, unixSecond, type CursorPage,
} from '@shared/operations/wire';

export const REPORT_STATUSES = ['pending_indexing', 'pending_review', 'approved_processing', 'approved', 'rejected', 'expired'] as const;
export type ReportStatus = typeof REPORT_STATUSES[number];

export interface ReportBadge {
  total: string;
  by_status: { pending_indexing: string; pending_review: string; approved_processing: string };
}

export function normalizeReportBadge(value: unknown): ReportBadge {
  const root = record(value, ['total', 'by_status'], 'report badge');
  const status = record(root.by_status, ['pending_indexing', 'pending_review', 'approved_processing'], 'report badge statuses');
  const result = {
    total: decimal(root.total, 'report badge total'),
    by_status: {
      pending_indexing: decimal(status.pending_indexing, 'report indexing count'),
      pending_review: decimal(status.pending_review, 'report review count'),
      approved_processing: decimal(status.approved_processing, 'report processing count'),
    },
  };
  if (BigInt(result.total) !== BigInt(result.by_status.pending_indexing) + BigInt(result.by_status.pending_review) + BigInt(result.by_status.approved_processing)) invalidResponse('report badge total');
  return result;
}

export interface ReportCaseSummary {
  id: string; status: ReportStatus; progress_state: 'in_progress' | 'complete';
  connector_type: 'openai-compatible' | 'anthropic-compatible'; canonical_base_url: string;
  material_version: string; target_version: string; deadline: number;
  counts: { materials: string; targets: string; distinct_owners: string; processed: string; deleted: string; released: string };
  retry: { attempt_count: string; next_attempt_at: number; last_error_class: 'db_busy' | 'internal_retryable' | 'invariant_violation' } | null;
  created_at: number; terminal_at: number | null;
}

export function normalizeReportSummary(value: unknown): ReportCaseSummary {
  const root = record(value, ['id', 'status', 'progress_state', 'connector_type', 'canonical_base_url', 'material_version', 'target_version', 'deadline', 'counts', 'retry', 'created_at', 'terminal_at'], 'report case');
  const status = oneOf(root.status, REPORT_STATUSES, 'report case status');
  const progress = oneOf(root.progress_state, ['in_progress', 'complete'] as const, 'report progress');
  if ((status === 'pending_indexing' || status === 'approved_processing') && progress !== 'in_progress') invalidResponse('report progress state');
  if ((status === 'pending_review' || status === 'approved' || status === 'rejected') && progress !== 'complete') invalidResponse('report progress state');
  const counts = record(root.counts, ['materials', 'targets', 'distinct_owners', 'processed', 'deleted', 'released'], 'report counts');
  const normalizedCounts = {
    materials: decimal(counts.materials, 'report material count'), targets: decimal(counts.targets, 'report target count'),
    distinct_owners: decimal(counts.distinct_owners, 'report owner count'), processed: decimal(counts.processed, 'report processed count'),
    deleted: decimal(counts.deleted, 'report deleted count'), released: decimal(counts.released, 'report released count'),
  };
  if (BigInt(normalizedCounts.processed) > BigInt(normalizedCounts.targets)
      || BigInt(normalizedCounts.deleted) > BigInt(normalizedCounts.targets)
      || BigInt(normalizedCounts.released) > BigInt(normalizedCounts.targets)) invalidResponse('report target counts');
  let retry: ReportCaseSummary['retry'] = null;
  if (root.retry !== null) {
    const item = record(root.retry, ['attempt_count', 'next_attempt_at', 'last_error_class'], 'report retry');
    retry = {
      attempt_count: decimal(item.attempt_count, 'report retry count', { positive: true }),
      next_attempt_at: unixSecond(item.next_attempt_at, 'report retry time'),
      last_error_class: oneOf(item.last_error_class, ['db_busy', 'internal_retryable', 'invariant_violation'] as const, 'report retry class'),
    };
  }
  const terminal = nullableUnixSecond(root.terminal_at, 'report terminal time');
  const terminalStatus = status === 'approved' || status === 'rejected' || status === 'expired';
  if (terminalStatus !== (terminal !== null)) invalidResponse('report terminal state');
  return {
    id: opaqueID(root.id, 'rpc_', 'report case id'), status, progress_state: progress,
    connector_type: oneOf(root.connector_type, ['openai-compatible', 'anthropic-compatible'] as const, 'reported connector'),
    canonical_base_url: string(root.canonical_base_url, 'reported canonical URL', { min: 1, max: 4_096, bytes: 4_096 }),
    material_version: decimal(root.material_version, 'report material version', { positive: true }),
    target_version: decimal(root.target_version, 'report target version', { positive: true }),
    deadline: unixSecond(root.deadline, 'report deadline'), counts: normalizedCounts, retry,
    created_at: unixSecond(root.created_at, 'report creation time'), terminal_at: terminal,
  };
}

export interface ReportMaterial {
  id: string; note_text: string; reporter: { user_id: string; discord_id: string } | null;
  source_ip: string; created_at: number;
}
function normalizeMaterial(value: unknown): ReportMaterial {
  const root = record(value, ['id', 'note_text', 'reporter', 'source_ip', 'created_at'], 'report material');
  let reporter: ReportMaterial['reporter'] = null;
  if (root.reporter !== null) {
    const item = record(root.reporter, ['user_id', 'discord_id'], 'reporter');
    reporter = { user_id: decimalID(item.user_id, 'reporter user id'), discord_id: string(item.discord_id, 'reporter Discord id', { min: 1, max: 64, bytes: 64, ascii: true }) };
  }
  return {
    id: decimalID(root.id, 'report material id'), note_text: string(root.note_text, 'report note', { max: 2_048, bytes: 8_192, multiline: true }),
    reporter, source_ip: string(root.source_ip, 'report source IP', { min: 2, max: 64, bytes: 64, ascii: true }),
    created_at: unixSecond(root.created_at, 'report material time'),
  };
}

export interface ReportCaseDetail extends ReportCaseSummary {
  materials: CursorPage<ReportMaterial>;
  decision: { action: 'approve' | 'reject' | 'expire' | 'resume_processing'; reason: string; actor_user_id: string | null; created_at: number } | null;
}
export function normalizeReportDetail(value: unknown): ReportCaseDetail {
  const root = record(value, ['id', 'status', 'progress_state', 'connector_type', 'canonical_base_url', 'material_version', 'target_version', 'deadline', 'counts', 'retry', 'created_at', 'terminal_at', 'materials', 'decision'], 'report case detail');
  const summary = normalizeReportSummary(Object.fromEntries(Object.entries(root).filter(([key]) => key !== 'materials' && key !== 'decision')));
  let decision: ReportCaseDetail['decision'] = null;
  if (root.decision !== null) {
    const item = record(root.decision, ['action', 'reason', 'actor_user_id', 'created_at'], 'report decision');
    decision = {
      action: oneOf(item.action, ['approve', 'reject', 'expire', 'resume_processing'] as const, 'report decision action'),
      reason: string(item.reason, 'report decision reason', { max: 1_024, bytes: 4_096, multiline: true }),
      actor_user_id: nullableDecimalID(item.actor_user_id, 'report decision actor'),
      created_at: unixSecond(item.created_at, 'report decision time'),
    };
  }
  return { ...summary, materials: page(root.materials, 'report material page', normalizeMaterial), decision };
}

export interface ReportTarget {
  id: string; target_seq: string; state: 'protected' | 'deleted_by_owner' | 'deleted_by_account' | 'deleted_by_approval' | 'released';
  endpoint_key_id: string | null; key_ref: string;
  owner: { user_id: string; discord_id: string; display_name: string } | null;
  endpoint: { connector_type: 'openai-compatible' | 'anthropic-compatible'; canonical_base_url: string; display_head: string; display_tail: string };
  discovered_version: string; decided_version: string | null; created_at: number; updated_at: number;
}
export function normalizeReportTarget(value: unknown): ReportTarget {
  const root = record(value, ['id', 'target_seq', 'state', 'endpoint_key_id', 'key_ref', 'owner', 'endpoint', 'discovered_version', 'decided_version', 'created_at', 'updated_at'], 'report target');
  let owner: ReportTarget['owner'] = null;
  if (root.owner !== null) {
    const item = record(root.owner, ['user_id', 'discord_id', 'display_name'], 'report target owner');
    owner = { user_id: decimalID(item.user_id, 'target owner id'), discord_id: string(item.discord_id, 'target owner Discord id', { min: 1, max: 64, bytes: 64, ascii: true }), display_name: string(item.display_name, 'target owner display name', { max: 128, bytes: 512 }) };
  }
  const endpoint = record(root.endpoint, ['connector_type', 'canonical_base_url', 'display_head', 'display_tail'], 'report target endpoint');
  const keyRef = string(root.key_ref, 'report key reference', { min: 43, max: 43, bytes: 43, ascii: true });
  if (!/^[A-Za-z0-9_-]{43}$/.test(keyRef)) invalidResponse('report key reference');
  const state = oneOf(root.state, ['protected', 'deleted_by_owner', 'deleted_by_account', 'deleted_by_approval', 'released'] as const, 'report target state');
  const decided = root.decided_version === null ? null : decimal(root.decided_version, 'target decision version', { positive: true });
  if ((state === 'protected') !== (decided === null)) invalidResponse('report target decision state');
  return {
    id: opaqueID(root.id, 'rpt_', 'report target id'), target_seq: decimal(root.target_seq, 'report target sequence', { positive: true }), state,
    endpoint_key_id: root.endpoint_key_id === null ? null : decimalID(root.endpoint_key_id, 'target endpoint key id'), key_ref: keyRef, owner,
    endpoint: {
      connector_type: oneOf(endpoint.connector_type, ['openai-compatible', 'anthropic-compatible'] as const, 'target connector'),
      canonical_base_url: string(endpoint.canonical_base_url, 'target canonical URL', { min: 1, max: 4_096, bytes: 4_096 }),
      display_head: string(endpoint.display_head, 'target key head', { max: 16, bytes: 16, ascii: true }),
      display_tail: string(endpoint.display_tail, 'target key tail', { max: 16, bytes: 16, ascii: true }),
    },
    discovered_version: decimal(root.discovered_version, 'target discovered version', { positive: true }), decided_version: decided,
    created_at: unixSecond(root.created_at, 'target creation time'), updated_at: unixSecond(root.updated_at, 'target update time'),
  };
}

export const adminReportKeys = {
  badge: ['admin', 'operations', 'reports', 'badge'] as const,
  list: (status: string, cursor: string | null) => ['admin', 'operations', 'reports', 'list', status, cursor] as const,
  detail: (id: string, cursor: string | null) => ['admin', 'operations', 'reports', 'detail', id, cursor] as const,
  targets: (id: string, cursor: string | null) => ['admin', 'operations', 'reports', 'targets', id, cursor] as const,
};
export const getReportBadge = () => decoded('/admin/api/reports/badge', normalizeReportBadge);
export const getReports = (status: string, cursor: string | null) => decoded(queryPath('/admin/api/reports', { status: status || undefined, cursor, limit: 50 }), (value) => page(value, 'report case page', normalizeReportSummary));
export const getReportDetail = (id: string, cursor: string | null) => decoded(queryPath(`/admin/api/reports/${encodeURIComponent(opaqueID(id, 'rpc_', 'report case id'))}`, { materials_cursor: cursor, materials_limit: 50 }), normalizeReportDetail);
export const getReportTargets = (id: string, cursor: string | null) => decoded(queryPath(`/admin/api/reports/${encodeURIComponent(opaqueID(id, 'rpc_', 'report case id'))}/targets`, { cursor, limit: 50 }), (value) => page(value, 'report target page', normalizeReportTarget));

export interface ReportDecisionReceipt {
  id: string;
  status: 'approved_processing' | 'rejected';
  material_version: string;
  target_version: string;
}

export function normalizeReportDecisionReceipt(
  value: unknown,
  expectedStatus: ReportDecisionReceipt['status'],
): ReportDecisionReceipt {
  const root = record(value, ['id', 'status', 'material_version', 'target_version'], 'report decision receipt');
  const status = oneOf(root.status, ['approved_processing', 'rejected'] as const, 'report decision receipt status');
  if (status !== expectedStatus) invalidResponse('report decision receipt status');
  return {
    id: opaqueID(root.id, 'rpc_', 'report decision receipt id'),
    status,
    material_version: decimal(root.material_version, 'report decision material version', { positive: true }),
    target_version: decimal(root.target_version, 'report decision target version', { positive: true }),
  };
}

export function approveReport(id: string, body: unknown, key: string): Promise<ReportDecisionReceipt> {
  return decoded(
    `/admin/api/reports/${encodeURIComponent(opaqueID(id, 'rpc_', 'report case id'))}/approve`,
    (value) => normalizeReportDecisionReceipt(value, 'approved_processing'),
    idempotentOptions(key, { method: 'POST', json: body }),
  );
}

export function rejectReport(id: string, body: unknown, key: string): Promise<ReportDecisionReceipt> {
  return decoded(
    `/admin/api/reports/${encodeURIComponent(opaqueID(id, 'rpc_', 'report case id'))}/reject`,
    (value) => normalizeReportDecisionReceipt(value, 'rejected'),
    idempotentOptions(key, { method: 'POST', json: body }),
  );
}

export function resumeReport(id: string, targetVersion: string, key: string): Promise<ReportDecisionReceipt> {
  return decoded(
    `/admin/api/reports/${encodeURIComponent(opaqueID(id, 'rpc_', 'report case id'))}/resume`,
    (value) => normalizeReportDecisionReceipt(value, 'approved_processing'),
    idempotentOptions(key, { method: 'POST', json: { expected_target_version: targetVersion } }),
  );
}
