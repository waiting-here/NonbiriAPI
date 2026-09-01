import { useEffect, useState } from 'react';
import { Link, useParams } from 'react-router';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { clearStationSession } from '@shared/charityManagement';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { Card, EmptyState, ErrorState, LoadingState, PageHeader, StatusBadge } from '@shared/components/States';
import { CursorPagination } from '@shared/operations/CursorPagination';
import { useCursorPager } from '@shared/operations/useCursorPager';
import { formatDateTime } from '@shared/utils/datetime';
import { isForbidden, isNotFoundError, isUnauthorized } from '@shared/query/http';
import { adminReportKeys, approveReport, getReportDetail, getReportTargets, rejectReport, resumeReport } from '../features/operations/reports';
import { useRetainedOperation } from '../features/operations/useRetainedOperation';
import '@shared/operations/operations.css';

type Decision = 'approve' | 'reject' | null;

export function ReportDetailPage() {
  const { caseId = '' } = useParams();
  const client = useQueryClient();
  const materials = useCursorPager();
  const targets = useCursorPager();
  const [reason, setReason] = useState('');
  const [confirmation, setConfirmation] = useState(false);
  const [decision, setDecision] = useState<Decision>(null);
  const detail = useQuery({ queryKey: adminReportKeys.detail(caseId, materials.cursor), queryFn: () => getReportDetail(caseId, materials.cursor), retry: false, enabled: Boolean(caseId) });
  const targetList = useQuery({ queryKey: adminReportKeys.targets(caseId, targets.cursor), queryFn: () => getReportTargets(caseId, targets.cursor), retry: false, enabled: Boolean(caseId) });
  const reconcile = async () => {
    await Promise.all([detail.refetch(), targetList.refetch(), client.invalidateQueries({ queryKey: adminReportKeys.badge })]);
  };
  const decide = useRetainedOperation(
    async (input: { action: 'approve' | 'reject'; material: string; target: string; reason: string; confirmed: boolean }, key) => {
      if (input.action === 'approve') return approveReport(caseId, { expected_material_version: input.material, expected_target_version: input.target, reason: input.reason, confirmation: input.confirmed }, key);
      return rejectReport(caseId, { expected_material_version: input.material, expected_target_version: input.target, reason: input.reason }, key);
    },
    async () => { await reconcile(); },
  );
  const resume = useRetainedOperation(
    (input: { target: string }, key) => resumeReport(caseId, input.target, key),
    async () => { await reconcile(); },
  );
  const resetDecision = decide.reset;
  const resetResume = resume.reset;

  useEffect(() => {
    const error = detail.error ?? targetList.error;
    if (error) {
      // A retained cache entry is not authority after a failed refresh.
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setDecision(null);
      setConfirmation(false);
    }
    if (isUnauthorized(error) || isForbidden(error)) {
      clearStationSession(client, 'admin');
      setReason('');
      resetDecision();
      resetResume();
    } else if (isNotFoundError(error)) {
      setReason('');
      resetDecision();
      resetResume();
      client.removeQueries({ queryKey: adminReportKeys.detail(caseId, materials.cursor), exact: true });
      client.removeQueries({ queryKey: adminReportKeys.targets(caseId, targets.cursor), exact: true });
    }
  }, [caseId, client, detail.error, materials.cursor, resetDecision, resetResume, targetList.error, targets.cursor]);

  const authorityBlocked = Boolean(detail.error || targetList.error);

  return (
    <div className="page ops-page">
      <PageHeader title="Report case" description="Review bounded evidence and stable target snapshots before one terminal decision." back={<Link to="/reports">Back to report inbox</Link>} />
      {detail.isPending ? <LoadingState /> : detail.error ? <ErrorState error={detail.error} onRetry={() => void detail.refetch()} /> : <>
        <Card><h2>Case authority</h2><dl className="ops-kv"><dt>ID</dt><dd>{detail.data.id}</dd><dt>Status</dt><dd><StatusBadge active={detail.data.status === 'pending_review'} danger={detail.data.status === 'approved_processing'} label={detail.data.status} /></dd><dt>Progress</dt><dd>{detail.data.progress_state}</dd><dt>Connector</dt><dd>{detail.data.connector_type}</dd><dt>Canonical URL</dt><dd>{detail.data.canonical_base_url}</dd><dt>Material / target revision</dt><dd>{detail.data.material_version} / {detail.data.target_version}</dd><dt>Deadline</dt><dd>{formatDateTime(detail.data.deadline)}</dd></dl>{detail.data.retry ? <p className="inline-notice">Retry {detail.data.retry.attempt_count}: {detail.data.retry.last_error_class}; next {formatDateTime(detail.data.retry.next_attempt_at)}</p> : null}</Card>
        {detail.data.decision ? <Card><h2>Recorded decision</h2><dl className="ops-kv"><dt>Action</dt><dd>{detail.data.decision.action}</dd><dt>Reason</dt><dd className="ops-wrap">{detail.data.decision.reason}</dd><dt>Actor</dt><dd>{detail.data.decision.actor_user_id ?? 'Deidentified'}</dd><dt>Recorded</dt><dd>{formatDateTime(detail.data.decision.created_at)}</dd></dl></Card> : null}
        <Card><h2>Materials</h2>{detail.data.materials.data.length === 0 ? <EmptyState title="No review materials" body="This authority page contains no materials." /> : <div className="ops-stack">{detail.data.materials.data.map((item) => <article className="card" key={item.id}><p>{item.note_text || 'No reporter note.'}</p><dl className="ops-kv"><dt>Reporter</dt><dd>{item.reporter ? `${item.reporter.user_id} / ${item.reporter.discord_id}` : 'Anonymous'}</dd><dt>Source IP</dt><dd>{item.source_ip}</dd><dt>Submitted</dt><dd>{formatDateTime(item.created_at)}</dd></dl></article>)}</div>}<CursorPagination page={materials.page} nextCursor={detail.data.materials.next_cursor} onPrevious={materials.previous} onNext={materials.next} /></Card>
      </>}
      <Card><h2>Targets</h2>{targetList.isPending ? <LoadingState /> : targetList.error ? <ErrorState error={targetList.error} onRetry={() => void targetList.refetch()} /> : targetList.data.data.length === 0 ? <EmptyState title="No targets" body="Indexing has not produced a target on this authority page." /> : <><div className="ops-table-scroll"><table className="ops-table"><thead><tr><th>Sequence</th><th>State</th><th>Owner</th><th>Endpoint</th><th>Key snapshot</th></tr></thead><tbody>{targetList.data.data.map((target) => <tr key={target.id}><td>{target.target_seq}</td><td>{target.state}</td><td>{target.owner ? `${target.owner.display_name} (${target.owner.user_id})` : 'Deidentified'}</td><td>{target.endpoint.connector_type}<br />{target.endpoint.canonical_base_url}</td><td>{target.endpoint.display_head}…{target.endpoint.display_tail}<br /><small>{target.key_ref}</small></td></tr>)}</tbody></table></div><CursorPagination page={targets.page} nextCursor={targetList.data.next_cursor} onPrevious={targets.previous} onNext={targets.next} /></>}</Card>
      {!authorityBlocked && detail.data?.status === 'pending_review' ? <Card className="ops-danger"><h2>Decision</h2><p>Approval permanently deletes matching protected endpoint keys through the integrated lifecycle. Rejection releases protection. Both require a reason and are never auto-retried after conflict or an unknown response.</p><label className="ops-form-field"><span>Reason</span><textarea value={reason} maxLength={1024} onChange={(event) => setReason(event.target.value)} /></label>{decide.error ? <ErrorState error={decide.error} /> : null}<div className="ops-actions"><button className="btn btn-danger" type="button" disabled={!reason.trim() || decide.isPending || resume.isPending} onClick={() => { setConfirmation(false); setDecision('approve'); }}>Approve deletion</button><button className="btn btn-secondary" type="button" disabled={!reason.trim() || decide.isPending || resume.isPending} onClick={() => setDecision('reject')}>Reject report</button></div></Card> : null}
      {!authorityBlocked && detail.data?.status === 'approved_processing' ? <Card className="ops-danger"><h2>Processing continuation</h2><p>This state cannot be re-decided or released. Resume only restarts the existing checkpoint with the same target version.</p>{resume.error ? <ErrorState error={resume.error} /> : null}<button className="btn btn-danger" type="button" disabled={resume.isPending || decide.isPending || !detail.data.retry} onClick={() => { const report = detail.data; if (report) resume.mutate({ target: report.target_version }); }}>Resume checkpoint</button></Card> : null}
      {decision && detail.data && !authorityBlocked ? <ConfirmDialog open title={decision === 'approve' ? 'Approve protected-key deletion?' : 'Reject this report?'} description={decision === 'approve' ? 'This accepts irreversible deletion work for the current material and target revisions.' : 'This releases all protected targets and records the review reason.'} confirmLabel={decision === 'approve' ? 'Approve deletion' : 'Reject report'} confirmDisabled={decision === 'approve' && !confirmation} danger busy={decide.isPending} onCancel={() => { setDecision(null); setConfirmation(false); }} onConfirm={() => { if (decision === 'approve' && !confirmation) return; const report = detail.data; if (!report) return; const action = decision; setDecision(null); decide.mutate({ action, material: report.material_version, target: report.target_version, reason: reason.trim(), confirmed: action === 'approve' }); }}>
        {decision === 'approve' ? <label><input type="checkbox" checked={confirmation} onChange={(event) => setConfirmation(event.target.checked)} /> I understand approved processing deletes matching endpoint keys.</label> : null}
      </ConfirmDialog> : null}
    </div>
  );
}
