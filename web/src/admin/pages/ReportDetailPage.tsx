import { StatusBadge } from '@shared/components/ui/StatusBadge';
import { reportStatusTone } from '../features/operations/reports';
import { DataTable } from '@shared/components/ui/DataTable';
import { FormActions } from '@shared/components/ui/Fields';
import { Field } from '@shared/components/ui/Field';
import { GatewayConnectorLabel } from '@shared/components/GatewayConnectorLabel';
import { useCallback, useEffect, useState } from 'react';
import { Link, useLocation, useParams } from 'react-router';
import { useSearchState } from '@shared/operations/useSearchState';
import { useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { clearStationSession, stationSessionWrite } from '@shared/charityManagement';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { Card, EmptyState, ErrorState, LoadingState, PageHeader } from '@shared/components/States';
import { PagePagination } from '@shared/operations/PagePagination';
import { useUrlPagePager } from '@shared/operations/useUrlPagePager';
import { listReturnPath } from '@shared/operations/listReturn';
import { useDateTimeFormatter } from '@shared/utils/datetime';
import { isForbidden, isUnauthorized } from '@shared/query/http';
import { useAdminSession } from '../data';
import {
  approveReport,
  REPORT_DONATION_ENDED_REASONS,
  rejectReport,
  resumeReport,
  type ReportCaseDetail,
  type ReportCaseSummary,
  type ReportDonationMatch,
  type ReportStatus,
  type ReportTarget,
} from '../features/operations/reports';
import {
  reportPageKeys,
  useReportDetailPage,
  useReportTargetDonationsPage,
  useReportTargetsPage,
} from '../features/operations/reportPages';
import { useRetainedOperation } from '../features/operations/useRetainedOperation';
import '@shared/operations/operations.css';

type Decision = 'approve' | 'reject' | null;

export function ReportDetailPage() {
  const { caseId = '' } = useParams();
  const session = useAdminSession();
  if (session.error)
    return <ErrorState error={session.error} onRetry={() => void session.refetch()} />;
  if (!session.data) return <LoadingState />;
  return (
    <ReportDetail
      key={`${session.data.admin.username}:${caseId}`}
      accountId={session.data.admin.username}
      caseId={caseId}
    />
  );
}

function ReportDetail({ accountId, caseId }: { accountId: string; caseId: string }) {
  const formatDateTime = useDateTimeFormatter();
  const { t } = useTranslation();
  const location = useLocation();
  const [searchParams, setSearchParams] = useSearchState();
  const client = useQueryClient();
  const [authorityError, setAuthorityError] = useState<unknown>(null);
  const scopeReady = Boolean(accountId) && !authorityError;
  const lineageTargetValues = searchParams.getAll('lineage_target');
  const urlLineageTargetId =
    lineageTargetValues.length === 1 &&
    /^rpt_[A-Za-z0-9_-]{22}$/.test(lineageTargetValues[0]) &&
    /[AQgw]$/.test(lineageTargetValues[0])
      ? lineageTargetValues[0]
      : null;
  const materials = useUrlPagePager({
    station: 'admin',
    listType: 'report-materials',
    scopeKey: `${accountId || 'anonymous'}:${caseId}`,
    scopeReady,
    resetKey: caseId,
    pageParam: 'materials_page',
    pageSizeParam: 'materials_page_size',
  });
  const targets = useUrlPagePager({
    station: 'admin',
    listType: 'report-targets',
    scopeKey: `${accountId || 'anonymous'}:${caseId}`,
    scopeReady,
    resetKey: caseId,
    pageParam: 'targets_page',
    pageSizeParam: 'targets_page_size',
  });
  const lineage = useUrlPagePager({
    station: 'admin',
    listType: 'report-lineage',
    scopeKey: `${accountId}:${caseId}`,
    scopeReady,
    pageParam: 'lineage_page',
    pageSizeParam: 'lineage_page_size',
  });
  const [confirmResume, setConfirmResume] = useState(false);
  const [reason, setReason] = useState('');
  const [decision, setDecision] = useState<Decision>(null);
  const revealLineage = useCallback(
    (node: HTMLHeadingElement | null) => {
      if (node && urlLineageTargetId) {
        node.focus({ preventScroll: true });
        node.scrollIntoView?.({ block: 'nearest' });
      }
    },
    [urlLineageTargetId],
  );
  const [lineageTarget, setLineageTarget] = useState<ReportTarget | null>(null);
  const detail = useReportDetailPage(
    accountId,
    caseId,
    materials.page,
    materials.pageSize,
    scopeReady,
  );
  const targetList = useReportTargetsPage(
    accountId,
    caseId,
    targets.page,
    targets.pageSize,
    scopeReady,
  );
  const targetDonations = useReportTargetDonationsPage(
    accountId,
    caseId,
    urlLineageTargetId ?? '',
    lineage.page,
    lineage.pageSize,
    scopeReady,
  );
  const effectiveLineageTargetId = urlLineageTargetId;
  const displayedLineageTarget =
    (lineageTarget?.id === effectiveLineageTargetId ? lineageTarget : undefined) ??
    targetList.data?.data.find((target) => target.id === effectiveLineageTargetId);
  const reconcile = async () => {
    return Promise.all([
      detail.refetch({ cancelRefetch: false }),
      targetList.refetch({ cancelRefetch: false }),
      effectiveLineageTargetId
        ? targetDonations.refetch({ cancelRefetch: false })
        : Promise.resolve(),
      client.invalidateQueries({ queryKey: reportPageKeys.badge(accountId) }),
      client.invalidateQueries({ queryKey: reportPageKeys.lists(accountId) }),
    ]);
  };
  const retryAuthority = async () => {
    const results = await reconcile();
    const failed = results.find((result) => result && result.error);
    setAuthorityError(failed?.error ?? null);
  };
  const decide = useRetainedOperation(
    async (
      input: {
        action: 'approve' | 'reject';
        material: string;
        target: string;
        reason: string;
        confirmed: boolean;
      },
      key,
    ) =>
      stationSessionWrite(client, 'admin', async () => {
        if (input.action === 'approve')
          return approveReport(
            caseId,
            {
              expected_material_version: input.material,
              expected_target_version: input.target,
              reason: input.reason,
              confirmation: input.confirmed,
            },
            key,
          );
        return rejectReport(
          caseId,
          {
            expected_material_version: input.material,
            expected_target_version: input.target,
            reason: input.reason,
          },
          key,
        );
      }),
    async () => {
      await reconcile();
    },
  );
  const resume = useRetainedOperation(
    (input: { target: string }, key) =>
      stationSessionWrite(client, 'admin', () => resumeReport(caseId, input.target, key)),
    async () => {
      await reconcile();
    },
  );
  const resetDecision = decide.reset;
  const resetResume = resume.reset;

  useEffect(() => {
    const errors = [
      detail.error,
      targetList.error,
      targetDonations.error,
      decide.error,
      resume.error,
    ];
    const error =
      errors.find((value) => isForbidden(value) || isUnauthorized(value)) ?? errors.find(Boolean);
    if (error) {
      // A retained cache entry is not authority after a failed refresh.
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setDecision(null);
    }
    if (isUnauthorized(error) || isForbidden(error)) {
      setAuthorityError(error);
      if (isUnauthorized(error)) clearStationSession(client, 'admin');
      setReason('');
      resetDecision();
      resetResume();
      setLineageTarget(null);
      setSearchParams(
        (current) => {
          const next = new URLSearchParams(current);
          for (const key of ['lineage_target', 'lineage_page', 'lineage_page_size'])
            next.delete(key);
          return next;
        },
        { replace: true },
      );
    }
  }, [
    accountId,
    caseId,
    client,
    detail.error,
    resetDecision,
    resetResume,
    decide.error,
    resume.error,
    setSearchParams,
    targetList.error,
    targetDonations.error,
  ]);

  const lineageAuthorityBlocked =
    isUnauthorized(targetDonations.error) || isForbidden(targetDonations.error);
  const authorityBlocked = Boolean(
    detail.error ||
    targetList.error ||
    lineageAuthorityBlocked ||
    detail.isFetching ||
    targetList.isFetching,
  );
  const readAuthorityError =
    authorityError ??
    [detail.error, targetList.error, targetDonations.error, decide.error, resume.error].find(
      (error) => isForbidden(error) || isUnauthorized(error),
    );
  if (readAuthorityError)
    return <ErrorState error={readAuthorityError} onRetry={() => void retryAuthority()} />;
  const statusLabels: Record<ReportStatus, string> = {
    pending_indexing: t('admin.reports.status.pendingIndexing'),
    pending_review: t('admin.reports.status.pendingReview'),
    approved_processing: t('admin.reports.status.approvedProcessing'),
    approved: t('admin.reports.status.approved'),
    rejected: t('admin.reports.status.rejected'),
    expired: t('admin.reports.status.expired'),
  };
  const progressLabels: Record<ReportCaseSummary['progress_state'], string> = {
    in_progress: t('admin.reports.progress.inProgress'),
    complete: t('admin.reports.progress.complete'),
  };
  const retryClassLabels: Record<
    NonNullable<ReportCaseSummary['retry']>['last_error_class'],
    string
  > = {
    db_busy: t('admin.reports.retryClass.databaseBusy'),
    internal_retryable: t('admin.reports.retryClass.internalRetryable'),
    invariant_violation: t('admin.reports.retryClass.invariantViolation'),
  };
  const decisionActionLabels: Record<NonNullable<ReportCaseDetail['decision']>['action'], string> =
    {
      approve: t('admin.reports.decisionAction.approve'),
      reject: t('admin.reports.decisionAction.reject'),
      expire: t('admin.reports.decisionAction.expire'),
      resume_processing: t('admin.reports.decisionAction.resumeProcessing'),
    };
  const targetStateLabels: Record<ReportTarget['state'], string> = {
    protected: t('admin.reports.targetState.protected'),
    deleted_by_owner: t('admin.reports.targetState.deletedByOwner'),
    deleted_by_account: t('admin.reports.targetState.deletedByAccount'),
    deleted_by_approval: t('admin.reports.targetState.deletedByApproval'),
    released: t('admin.reports.targetState.released'),
  };
  const donationStatusLabels: Record<ReportDonationMatch['donation_status'], string> = {
    pending: t('admin.reports.detail.lineage.status.pending'),
    approved: t('admin.reports.detail.lineage.status.approved'),
    rejected: t('admin.reports.detail.lineage.status.rejected'),
    deleted: t('admin.reports.detail.lineage.status.deleted'),
    expired: t('admin.reports.detail.lineage.status.expired'),
  };
  const donationKeyStateLabels: Record<ReportDonationMatch['key_state'], string> = {
    pending: t('admin.reports.detail.lineage.keyState.pending'),
    available: t('admin.reports.detail.lineage.keyState.available'),
    disabled: t('admin.reports.detail.lineage.keyState.disabled'),
    suspended: t('admin.reports.detail.lineage.keyState.suspended'),
    exhausted: t('admin.reports.detail.lineage.keyState.exhausted'),
    expired: t('admin.reports.detail.lineage.keyState.expired'),
    ended: t('admin.reports.detail.lineage.keyState.ended'),
  };
  const donationEndedReasonLabels: Record<(typeof REPORT_DONATION_ENDED_REASONS)[number], string> =
    {
      withdrawn: t('admin.reports.detail.lineage.endedReason.withdrawn'),
      terminated: t('admin.reports.detail.lineage.endedReason.terminated'),
      expired: t('admin.reports.detail.lineage.endedReason.expired'),
      member_removed: t('admin.reports.detail.lineage.endedReason.memberRemoved'),
      account_deleted: t('admin.reports.detail.lineage.endedReason.accountDeleted'),
    };

  return (
    <div className="page ops-page">
      <PageHeader
        title={t('admin.reports.detail.title')}
        description={t('admin.reports.detail.description')}
        back={
          <Link
            to={listReturnPath(location.state, '/reports')}
            state={{ listSearch: location.state?.listSearch }}
          >
            {t('admin.reports.detail.back')}
          </Link>
        }
      />
      {detail.isPending ? (
        <LoadingState />
      ) : detail.error ? (
        <ErrorState error={detail.error} onRetry={() => void detail.refetch()} />
      ) : (
        <>
          <Card>
            <h2>{t('admin.reports.detail.authorityTitle')}</h2>
            <dl className="ops-kv">
              <dt>{t('admin.reports.detail.id')}</dt>
              <dd>{detail.data.id}</dd>
              <dt>{t('admin.reports.detail.status')}</dt>
              <dd>
                <StatusBadge tone={reportStatusTone[detail.data.status]}>
                  {statusLabels[detail.data.status]}
                </StatusBadge>
              </dd>
              <dt>{t('admin.reports.detail.progress')}</dt>
              <dd>{progressLabels[detail.data.progress_state]}</dd>
              <dt>{t('admin.reports.detail.connector')}</dt>
              <dd>
                <GatewayConnectorLabel value={detail.data.connector_type} />
              </dd>
              <dt>{t('admin.reports.detail.canonicalUrl')}</dt>
              <dd>{detail.data.canonical_base_url}</dd>
              <dt>{t('admin.reports.detail.materialTargetRevision')}</dt>
              <dd>
                {detail.data.material_version} / {detail.data.target_version}
              </dd>
              <dt>{t('admin.reports.detail.deadline')}</dt>
              <dd>{formatDateTime(detail.data.deadline)}</dd>
            </dl>
            {detail.data.retry ? (
              <p className="inline-notice">
                {t('admin.reports.detail.retryAttempt', {
                  count: detail.data.retry.attempt_count,
                })}
                : {retryClassLabels[detail.data.retry.last_error_class]};{' '}
                {t('admin.reports.detail.retryNext')}{' '}
                {formatDateTime(detail.data.retry.next_attempt_at)}
              </p>
            ) : null}
          </Card>
          {detail.data.decision ? (
            <Card>
              <h2>{t('admin.reports.detail.recordedDecision')}</h2>
              <dl className="ops-kv">
                <dt>{t('admin.reports.detail.action')}</dt>
                <dd>{decisionActionLabels[detail.data.decision.action]}</dd>
                <dt>{t('admin.reports.detail.reason')}</dt>
                <dd className="ops-wrap">{detail.data.decision.reason}</dd>
                <dt>{t('admin.reports.detail.actor')}</dt>
                <dd>
                  {detail.data.decision.actor_user_id ?? t('admin.reports.detail.deidentified')}
                </dd>
                <dt>{t('admin.reports.detail.recorded')}</dt>
                <dd>{formatDateTime(detail.data.decision.created_at)}</dd>
              </dl>
            </Card>
          ) : null}
          <Card>
            <h2>{t('admin.reports.detail.materialsTitle')}</h2>
            <div className="ops-stack" aria-busy={detail.isFetching}>
              {detail.data.materials.data.length === 0 ? (
                <EmptyState
                  title={t('admin.reports.detail.materialsEmptyTitle')}
                  body={t('admin.reports.detail.materialsEmptyBody')}
                />
              ) : (
                <div className="ops-stack">
                  {detail.data.materials.data.map((item) => (
                    <article className="ops-subcard" key={item.id}>
                      <p>{item.note_text || t('admin.reports.detail.noReporterNote')}</p>
                      <dl className="nb-facts nb-facts--inline">
                        <dt>{t('admin.reports.detail.reporter')}</dt>
                        <dd>
                          {item.reporter ? (
                            <Link to={`/users?user=${item.reporter.user_id}`}>
                              {item.reporter.user_id} / {item.reporter.discord_id}
                            </Link>
                          ) : (
                            t('admin.reports.detail.anonymous')
                          )}
                        </dd>
                        <dt>{t('admin.reports.detail.sourceIp')}</dt>
                        <dd className="mono">{item.source_ip}</dd>
                        <dt>{t('admin.reports.detail.submitted')}</dt>
                        <dd>{formatDateTime(item.created_at)}</dd>
                      </dl>
                    </article>
                  ))}
                </div>
              )}
              <PagePagination
                metadata={detail.data.materials_pagination}
                requestedPage={materials.page}
                busy={detail.isFetching}
                onPageChange={materials.setPage}
                onPageSizeChange={materials.setPageSize}
              />
            </div>
          </Card>
        </>
      )}
      <Card>
        <h2>{t('admin.reports.detail.targetsTitle')}</h2>
        <div className="ops-stack" aria-busy={targetList.isFetching}>
          {targetList.isPending ? (
            <LoadingState />
          ) : targetList.error ? (
            <ErrorState error={targetList.error} onRetry={() => void targetList.refetch()} />
          ) : targetList.data.data.length === 0 ? (
            <>
              <EmptyState
                title={t('admin.reports.detail.targetsEmptyTitle')}
                body={t('admin.reports.detail.targetsEmptyBody')}
              />
              <PagePagination
                metadata={targetList.data.pagination}
                requestedPage={targets.page}
                busy={targetList.isFetching}
                onPageChange={targets.setPage}
                onPageSizeChange={targets.setPageSize}
              />
            </>
          ) : (
            <>
              <DataTable
                caption={t('admin.reports.detail.targetsTitle')}
                rows={targetList.data.data}
                rowKey={(target) => target.id}
                columns={[
                  {
                    key: 'sequence',
                    header: t('admin.reports.detail.sequence'),
                    cell: 'title',
                    render: (target) => `#${target.target_seq}`,
                  },
                  {
                    key: 'state',
                    header: t('admin.reports.detail.state'),
                    cell: 'status',
                    render: (target) => targetStateLabels[target.state],
                  },
                  {
                    key: 'owner',
                    header: t('admin.reports.detail.owner'),
                    mobileLabel: t('admin.reports.detail.owner'),
                    cell: 'meta',
                    render: (target) =>
                      target.owner
                        ? target.owner.display_name
                        : t('admin.reports.detail.deidentified'),
                  },
                  {
                    key: 'userId',
                    header: t('common.userId'),
                    mobileLabel: t('common.userId'),
                    cell: 'meta',
                    render: (target) =>
                      target.owner?.user_id ? (
                        <Link className="mono" to={`/users?user=${target.owner.user_id}`}>
                          {target.owner.user_id}
                        </Link>
                      ) : (
                        '—'
                      ),
                  },
                  {
                    key: 'endpoint',
                    header: t('admin.reports.detail.endpoint'),
                    mobileLabel: t('admin.reports.detail.endpoint'),
                    cell: 'meta',
                    render: (target) => (
                      <div className="nb-stack nb-stack--sm">
                        <GatewayConnectorLabel value={target.endpoint.connector_type} />
                        <span className="nb-small nb-muted ops-wrap">
                          {target.endpoint.canonical_base_url}
                        </span>
                      </div>
                    ),
                  },
                  {
                    key: 'keySnapshot',
                    header: t('admin.reports.detail.keySnapshot'),
                    mobileLabel: t('admin.reports.detail.keySnapshot'),
                    cell: 'meta',
                    render: (target) => (
                      <span className="mono">
                        {target.endpoint.display_head}…{target.endpoint.display_tail}
                      </span>
                    ),
                  },
                  {
                    key: 'keyReference',
                    header: t('admin.reports.detail.keyReference'),
                    mobileLabel: t('admin.reports.detail.keyReference'),
                    cell: 'meta',
                    render: (target) => <span className="mono">{target.key_ref}</span>,
                  },
                  {
                    key: 'donationMatches',
                    header: t('admin.reports.detail.donationMatches'),
                    mobileLabel: t('admin.reports.detail.donationMatches'),
                    cell: 'meta',
                    align: 'num',
                    render: (target) => target.donation_match_count,
                  },
                  {
                    key: 'lineageAction',
                    header: t('admin.reports.detail.lineageAction'),
                    cell: 'action',
                    render: (target) => (
                      <button
                        className="nb-btn nb-btn--secondary nb-btn--sm"
                        type="button"
                        disabled={targetList.isFetching}
                        onClick={() => {
                          setSearchParams((current) => {
                            const next = new URLSearchParams(current);
                            next.delete('lineage_target');
                            next.set('lineage_target', target.id);
                            next.delete('lineage_page');
                            next.set('lineage_page', '1');
                            next.delete('lineage_page_size');
                            next.set('lineage_page_size', String(lineage.pageSize));
                            return next;
                          });
                          setLineageTarget(target);
                        }}
                      >
                        {t('admin.reports.detail.openLineage')}
                      </button>
                    ),
                  },
                ]}
              />
              <PagePagination
                metadata={targetList.data.pagination}
                requestedPage={targets.page}
                busy={targetList.isFetching}
                onPageChange={targets.setPage}
                onPageSizeChange={targets.setPageSize}
              />
            </>
          )}
        </div>
      </Card>
      {effectiveLineageTargetId ? (
        <Card>
          <div className="ops-toolbar">
            <h2 tabIndex={-1} ref={revealLineage}>
              {t('admin.reports.detail.lineageTitle')}
            </h2>
            <button
              className="nb-btn nb-btn--secondary"
              type="button"
              onClick={() => {
                setLineageTarget(null);
                setSearchParams((current) => {
                  const next = new URLSearchParams(current);
                  for (const key of ['lineage_target', 'lineage_page', 'lineage_page_size']) {
                    next.delete(key);
                  }
                  return next;
                });
              }}
            >
              {t('common.close')}
            </button>
          </div>
          <p>
            {t('admin.reports.detail.lineageTarget', {
              sequence: displayedLineageTarget?.target_seq ?? effectiveLineageTargetId,
            })}
          </p>
          <div className="ops-stack" aria-busy={targetDonations.isFetching}>
            {targetDonations.isPending ? (
              <LoadingState />
            ) : targetDonations.error ? (
              <ErrorState
                error={targetDonations.error}
                onRetry={() => void targetDonations.refetch()}
              />
            ) : targetDonations.data.data.length === 0 ? (
              <>
                <EmptyState
                  title={t('admin.reports.detail.lineageEmptyTitle')}
                  body={t('admin.reports.detail.lineageEmptyBody')}
                />
                <PagePagination
                  metadata={targetDonations.data.pagination}
                  requestedPage={lineage.page}
                  busy={targetDonations.isFetching}
                  onPageChange={lineage.setPage}
                  onPageSizeChange={lineage.setPageSize}
                />
              </>
            ) : (
              <>
                <DataTable
                  caption={t('admin.reports.detail.lineageTitle')}
                  rows={targetDonations.data.data}
                  rowKey={(match) => match.donation_key_id}
                  columns={[
                    {
                      key: 'donation',
                      header: t('admin.reports.detail.lineage.donation'),
                      cell: 'title',
                      render: (match) => (
                        <Link className="mono" to={`/charity?donation_id=${match.donation_id}`}>
                          {match.donation_id}
                        </Link>
                      ),
                    },
                    {
                      key: 'key',
                      header: t('admin.reports.detail.lineage.key'),
                      mobileLabel: t('admin.reports.detail.lineage.key'),
                      cell: 'meta',
                      render: (match) => <span className="mono">{match.donation_key_id}</span>,
                    },
                    {
                      key: 'donationStatus',
                      header: t('admin.reports.detail.lineage.donationStatus'),
                      cell: 'status',
                      render: (match) => donationStatusLabels[match.donation_status],
                    },
                    {
                      key: 'keyStatus',
                      header: t('admin.reports.detail.lineage.keyStatus'),
                      mobileLabel: t('admin.reports.detail.lineage.keyStatus'),
                      cell: 'meta',
                      render: (match) => donationKeyStateLabels[match.key_state],
                    },
                    {
                      key: 'expires',
                      header: t('admin.reports.detail.lineage.expires'),
                      mobileLabel: t('admin.reports.detail.lineage.expires'),
                      cell: 'meta',
                      render: (match) =>
                        match.expires_at === null
                          ? t('admin.reports.detail.lineage.never')
                          : formatDateTime(match.expires_at),
                    },
                    {
                      key: 'ended',
                      header: t('admin.reports.detail.lineage.ended'),
                      mobileLabel: t('admin.reports.detail.lineage.ended'),
                      cell: 'meta',
                      render: (match) =>
                        match.ended_at === null
                          ? t('admin.reports.detail.lineage.notEnded')
                          : `${formatDateTime(match.ended_at)}${match.ended_reason ? ` · ${donationEndedReasonLabels[match.ended_reason]}` : ''}`,
                    },
                  ]}
                />
                <PagePagination
                  metadata={targetDonations.data.pagination}
                  requestedPage={lineage.page}
                  busy={targetDonations.isFetching}
                  onPageChange={lineage.setPage}
                  onPageSizeChange={lineage.setPageSize}
                />
              </>
            )}
          </div>
        </Card>
      ) : null}
      {!authorityBlocked && detail.data?.status === 'pending_review' ? (
        <Card className="ops-danger">
          <h2>{t('admin.reports.detail.decisionTitle')}</h2>
          <p>{t('admin.reports.detail.decisionBody')}</p>
          <Field
            label={
              <>
                {t('admin.reports.detail.reason')} <span aria-hidden="true">*</span>
              </>
            }
            size="long"
          >
            {(field) => (
              <textarea
                {...field}
                required
                value={reason}
                maxLength={1024}
                onChange={(event) => setReason(event.target.value)}
              />
            )}
          </Field>
          {decide.error ? <ErrorState error={decide.error} /> : null}
          <FormActions>
            <button
              className="nb-btn nb-btn--danger"
              type="button"
              disabled={!reason.trim() || decide.isPending || resume.isPending}
              onClick={() => {
                setDecision('approve');
              }}
            >
              {t('admin.reports.detail.approveDeletion')}
            </button>
            <button
              className="nb-btn nb-btn--secondary"
              type="button"
              disabled={!reason.trim() || decide.isPending || resume.isPending}
              onClick={() => setDecision('reject')}
            >
              {t('admin.reports.detail.rejectReport')}
            </button>
          </FormActions>
        </Card>
      ) : null}
      {!authorityBlocked && detail.data?.status === 'approved_processing' ? (
        <Card className="ops-danger">
          <h2>{t('admin.reports.detail.continuationTitle')}</h2>
          <p>{t('admin.reports.detail.continuationBody')}</p>
          {resume.error ? <ErrorState error={resume.error} /> : null}
          <button
            className="nb-btn nb-btn--danger"
            type="button"
            disabled={resume.isPending || decide.isPending || !detail.data.retry}
            onClick={() => setConfirmResume(true)}
          >
            {t('admin.reports.detail.resumeCheckpoint')}
          </button>
        </Card>
      ) : null}
      {confirmResume && detail.data && !authorityBlocked && (
        <ConfirmDialog
          open
          danger
          title={t('admin.reports.detail.resumeCheckpoint')}
          description={t('admin.audit.resumeReport', { id: detail.data.id })}
          confirmLabel={t('admin.reports.detail.resumeCheckpoint')}
          busy={resume.isPending}
          confirmDisabled={!detail.data.retry}
          onCancel={() => setConfirmResume(false)}
          onConfirm={() => {
            if (detail.data)
              resume.mutate(
                { target: detail.data.target_version },
                { onSuccess: () => setConfirmResume(false) },
              );
          }}
        >
          {resume.error && <ErrorState error={resume.error} />}
        </ConfirmDialog>
      )}
      {decision && detail.data && !authorityBlocked ? (
        <ConfirmDialog
          open
          title={
            decision === 'approve'
              ? t('admin.reports.detail.confirmApproveTitle')
              : t('admin.reports.detail.confirmRejectTitle')
          }
          description={
            decision === 'approve'
              ? t('admin.reports.detail.confirmApproveBody')
              : t('admin.reports.detail.confirmRejectBody')
          }
          confirmLabel={
            decision === 'approve'
              ? t('admin.reports.detail.approveDeletion')
              : t('admin.reports.detail.rejectReport')
          }
          danger
          busy={decide.isPending}
          onCancel={() => {
            setDecision(null);
          }}
          onConfirm={() => {
            const report = detail.data;
            if (!report) return;
            const action = decision;
            setDecision(null);
            decide.mutate({
              action,
              material: report.material_version,
              target: report.target_version,
              reason: reason.trim(),
              confirmed: action === 'approve',
            });
          }}
        >
          <p className="ops-blacklist-note">{reason}</p>
        </ConfirmDialog>
      ) : null}
    </div>
  );
}
