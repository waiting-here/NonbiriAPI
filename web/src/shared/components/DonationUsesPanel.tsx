import { ConnectionUnavailable } from './ConnectionUnavailable';
import { useSearchParams } from 'react-router';
import { UseDiscovery } from './UseDiscovery';
import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Field, Fold, Note, OutcomeNote, Panel, PanelBody, PanelHead } from './ui';
import { ErrorState, LoadingState } from './States';
import { ConfirmDialog } from './ConfirmDialog';
import { PagePagination } from '@shared/operations/PagePagination';
import { useUrlPagePager } from '@shared/operations/useUrlPagePager';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';
import { responseOutcomeUnknown } from '@shared/operations/api';
import {
  getDonationUses,
  mutateDonationUse,
  reviewDonationUses,
  type DonationUse,
  type UseReviewItem,
  type UseReviewResult,
  type UseRole,
  type UseVersion,
} from '@shared/operations/donationUses';
import { NATIVE_SERVICE_LABEL_KEYS } from '@shared/nativeServices';
import { formatCount } from '@shared/utils/formatNumber';

export function DonationUsesPanel({
  role,
  accountId,
  donationId,
  donationKeyId,
  onReviewed,
  onPendingChange,
}: {
  role: UseRole;
  accountId: string;
  donationId: string;
  donationKeyId?: string;
  onReviewed?: (results: UseReviewResult[]) => void;
  onPendingChange?: (pending: boolean) => void;
}) {
  const { t } = useTranslation();
  const client = useQueryClient();
  const root = role === 'admin' ? ['admin', 'operations'] : ['user'];
  const [params, setParams] = useSearchParams();
  const stateParam = donationKeyId ? 'uses_' + donationKeyId + '_state' : 'uses_state';
  const rawState = params.get(stateParam) ?? '';
  const state = ['pending', 'approved', 'rejected', 'withdrawn'].includes(rawState) ? rawState : '';
  const setState = (value: string) =>
    setParams((previous) => {
      const next = new URLSearchParams(previous);
      if (value) next.set(stateParam, value);
      else next.delete(stateParam);
      return next;
    });
  const [selected, setSelected] = useState<Record<string, DonationUse>>({});
  const [note, setNote] = useState('');
  const [results, setResults] = useState<UseReviewResult[]>([]);
  const result = useRef<UseReviewResult[]>([]);
  const pager = useUrlPagePager({
    station: role === 'admin' ? 'admin' : 'user',
    listType: 'donation-uses',
    scopeKey: accountId + ':' + donationId + ':' + (donationKeyId ?? 'all'),
    resetKey: state,
    pageParam: donationKeyId ? 'uses_' + donationKeyId + '_page' : 'uses_page',
    pageSizeParam: donationKeyId ? 'uses_' + donationKeyId + '_page_size' : 'uses_page_size',
  });
  const query = useQuery({
    queryKey: [
      ...root,
      'donation-uses',
      accountId,
      donationId,
      donationKeyId ?? 'all',
      state,
      pager.page,
      pager.pageSize,
    ],
    queryFn: ({ signal }) =>
      getDonationUses(role, donationId, pager.page, pager.pageSize, state, signal, donationKeyId),
  });
  const refresh = async () => {
    await client.invalidateQueries({ queryKey: root }, { throwOnError: true });
  };
  const review = useRetainedOperation<UseReviewItem[], UseReviewResult[]>(
    async (items, key, context) => {
      const value = await reviewDonationUses(
        role as 'admin' | 'steward',
        donationId,
        items,
        key,
        context.signal,
      );
      context.commit(() => {
        result.current = value;
      });
      return value;
    },
    async (items, error, context) => {
      if (error && responseOutcomeUnknown(error) && context.operationKey) {
        const value = await reviewDonationUses(
          role as 'admin' | 'steward',
          donationId,
          items,
          context.operationKey,
          context.signal,
        );
        context.commit(() => {
          result.current = value;
        });
      }
      if (!error || result.current.length) {
        context.commit(() => {
          setResults(result.current);
          onReviewed?.(result.current);
          setSelected((previous) =>
            Object.fromEntries(
              Object.entries(previous).filter(
                ([id]) => !result.current.some((row) => row.use_id === id && row.ok),
              ),
            ),
          );
        });
        await refresh();
        if (error) return { operationConfirmed: true };
      } else await refresh();
    },
    root,
  );
  useEffect(() => {
    onPendingChange?.(review.isPending);
    return () => onPendingChange?.(false);
  }, [onPendingChange, review.isPending]);
  const locked = review.isPending || review.outcome === 'unknown';
  const submit = (decision: 'approve' | 'reject') => {
    result.current = [];
    review.reset();
    void review
      .mutateAsync(
        Object.values(selected)
          .filter((row) => row.pending && row.capabilities.can_review)
          .map((row) => ({
            use_id: row.id,
            expected_revision: row.revision,
            expected_review_revision: row.pending!.expected_review_revision,
            decision,
            review_note: note.trim(),
          })),
      )
      .catch(() => undefined);
  };
  return (
    <Panel>
      <PanelHead title={t('common.connections.donationUses')} />
      <PanelBody>
        <Note>{t('common.connections.useVersionNote')}</Note>
        <Field label={t('common.connections.status')}>
          {(props) => (
            <select {...props} value={state} onChange={(event) => setState(event.target.value)}>
              <option value="">{t('common.connections.all')}</option>
              {['pending', 'approved', 'rejected', 'withdrawn'].map((value) => (
                <option key={value} value={value}>
                  {t(`common.connections.useStates.${value}`)}
                </option>
              ))}
            </select>
          )}
        </Field>
        {query.isPending ? (
          <LoadingState />
        ) : query.error ? (
          <ErrorState error={query.error} onRetry={() => void query.refetch()} />
        ) : query.data ? (
          <>
            <div className="nb-stack">
              {query.data.data.map((row) => (
                <UseRow
                  key={row.id}
                  role={role}
                  accountId={accountId}
                  row={row}
                  selected={Boolean(selected[row.id])}
                  disabled={locked}
                  selectionFull={!selected[row.id] && Object.keys(selected).length >= 100}
                  onSelect={(checked) =>
                    setSelected((previous) => {
                      const next = { ...previous };
                      if (checked && (previous[row.id] || Object.keys(previous).length < 100))
                        next[row.id] = row;
                      else delete next[row.id];
                      return next;
                    })
                  }
                  refresh={refresh}
                />
              ))}
            </div>
            <PagePagination
              metadata={query.data.pagination}
              requestedPage={pager.page}
              busy={query.isFetching || locked}
              onPageChange={pager.setPage}
              onPageSizeChange={pager.setPageSize}
            />
          </>
        ) : null}
        {role !== 'owner' && Object.keys(selected).length ? (
          <div className="nb-stack">
            <p>
              {t('common.connections.selectedUses', { count: Object.keys(selected).length })} ·{' '}
              {t('common.connections.reviewBatchLimit')}
            </p>
            <Field
              label={t('common.connections.reviewReason')}
              help={t('common.connections.reviewReasonVisible')}
            >
              {(props) => (
                <textarea
                  {...props}
                  value={note}
                  disabled={locked}
                  onChange={(event) => setNote(event.target.value)}
                />
              )}
            </Field>
            <div className="nb-actions">
              <button
                type="button"
                className="nb-btn nb-btn--primary"
                disabled={locked}
                onClick={() => submit('approve')}
              >
                {t('common.connections.approveSelected')}
              </button>
              <button
                type="button"
                className="nb-btn nb-btn--secondary"
                disabled={locked}
                onClick={() => submit('reject')}
              >
                {t('common.connections.rejectSelected')}
              </button>
            </div>
          </div>
        ) : null}
        {review.error ? <ErrorState error={review.error} /> : null}
        {review.outcome === 'unknown' ? (
          <Note tone="warn">
            <p>{t('common.connections.unknownResult')}</p>
            <button
              type="button"
              className="nb-btn nb-btn--secondary"
              disabled={review.isPending}
              onClick={() => void review.check().catch(() => undefined)}
            >
              {t('common.connections.checkResult')}
            </button>
          </Note>
        ) : null}
        {review.refreshError ? (
          review.isSuccess ? (
            <OutcomeNote
              busy={review.isPending}
              outcome={{ kind: 'savedRefreshFailed', recheck: () => void review.refresh() }}
            />
          ) : (
            <ErrorState error={review.refreshError} onRetry={() => void review.refresh()} />
          )
        ) : null}
        {!onReviewed ? <DonationUseReviewResults results={results} /> : null}
      </PanelBody>
    </Panel>
  );
}
export function DonationUseReviewResults({ results }: { results: UseReviewResult[] }) {
  const { t } = useTranslation();
  return results.length ? (
    <ul className="nb-list" role="status">
      {results.map((result) => (
        <li key={result.use_id}>
          {t(
            result.ok ? 'common.connections.useReviewSaved' : 'common.connections.useReviewFailed',
            { id: result.use_id },
          )}
          {result.error ? ' · ' + result.error.message : ''}
        </li>
      ))}
    </ul>
  ) : null;
}
function UseRow({
  role,
  accountId,
  row,
  selected,
  disabled,
  selectionFull,
  onSelect,
  refresh,
}: {
  role: UseRole;
  accountId: string;
  row: DonationUse;
  selected: boolean;
  disabled: boolean;
  selectionFull: boolean;
  onSelect: (value: boolean) => void;
  refresh: () => Promise<void>;
}) {
  const { t } = useTranslation();
  const [confirm, setConfirm] = useState<{ action: 'withdraw' | 'reset'; revision: string } | null>(
    null,
  );
  const root = role === 'admin' ? ['admin', 'operations'] : ['user'];
  type Intent = { action: 'patch' | 'withdraw' | 'reset'; input: Record<string, unknown> };
  const operation = useRetainedOperation<Intent, void>(
    async (intent, key, context) => {
      await mutateDonationUse(
        role,
        row.donation_id,
        row.id,
        intent.action,
        intent.input,
        key,
        context.signal,
      );
    },
    async (intent, error, context) => {
      let confirmed = !error;
      if (error && responseOutcomeUnknown(error) && context.operationKey) {
        await mutateDonationUse(
          role,
          row.donation_id,
          row.id,
          intent.action,
          intent.input,
          context.operationKey,
          context.signal,
        );
        confirmed = true;
      }
      await refresh();
      if (confirmed) {
        context.commit(() => setConfirm(null));
        if (error) return { operationConfirmed: true };
      }
    },
    root,
  );
  const locked =
    disabled ||
    operation.isPending ||
    operation.outcome === 'unknown' ||
    operation.outcome === 'refresh-failed';
  const run = (action: Intent['action'], input: Record<string, unknown> = {}) => {
    operation.reset();
    void operation
      .mutateAsync({ action, input: { ...input, expected_revision: row.revision } })
      .catch(() => undefined);
  };
  const feedback = (
    <>
      {operation.error ? <ErrorState error={operation.error} /> : null}
      {operation.outcome === 'unknown' ? (
        <button
          type="button"
          className="nb-btn nb-btn--secondary"
          disabled={operation.isPending}
          onClick={() => void operation.check().catch(() => undefined)}
        >
          {t('common.connections.checkResult')}
        </button>
      ) : null}
      {operation.refreshError ? (
        <ErrorState
          error={operation.refreshError}
          onRetry={() => void operation.refresh().catch(() => undefined)}
        />
      ) : null}
    </>
  );
  return (
    <Fold
      title={t(NATIVE_SERVICE_LABEL_KEYS[row.native_service])}
      summary={t('common.connections.useApprovalSummary', {
        approved: row.approved ? '1' : '0',
        pending: row.pending ? '1' : '0',
      })}
    >
      {role !== 'owner' && row.pending ? (
        <Field label={t('common.connections.selectUseReview')}>
          {(props) => (
            <input
              {...props}
              type="checkbox"
              checked={selected}
              disabled={locked || selectionFull || !row.capabilities.can_review}
              onChange={(event) => onSelect(event.target.checked)}
            />
          )}
        </Field>
      ) : null}
      {selected && row.pending && row.capabilities.can_review ? (
        <button
          type="button"
          className="nb-btn nb-btn--secondary"
          disabled={locked}
          onClick={() => onSelect(true)}
        >
          {t('common.connections.useCurrentRevision')}
        </button>
      ) : null}
      {row.approved ? (
        <VersionMaterial label={t('common.connections.approvedVersion')} value={row.approved} />
      ) : null}
      {row.pending ? (
        <VersionMaterial label={t('common.connections.pendingVersion')} value={row.pending} />
      ) : null}
      {!row.pending && row.latest && row.latest.status !== 'approved' ? (
        <VersionMaterial label={t('common.connections.latestDecision')} value={row.latest} />
      ) : null}
      {role !== 'owner' && row.approved?.discovery_source_id ? (
        <UseDiscovery role={role} accountId={accountId} row={row} />
      ) : null}
      {row.health ? (
        <Note>
          <p>{t('common.connections.serviceHealth')}</p>
          <p>
            {t('common.connections.failureCount', {
              count: formatCount(row.health.failure_streak).display,
            })}{' '}
            ·{' '}
            {row.health.failure_disabled
              ? t('common.connections.disabled')
              : t('common.connections.enabled')}
          </p>
        </Note>
      ) : null}
      {row.capabilities.unavailable_reason ? (
        <ConnectionUnavailable reason={row.capabilities.unavailable_reason} />
      ) : null}
      <div className="nb-actions">
        {role === 'owner' && row.capabilities.can_manage ? (
          <>
            <button
              type="button"
              className="nb-btn nb-btn--secondary"
              disabled={locked}
              onClick={() => run('patch', { enabled: !row.enabled })}
            >
              {row.enabled ? t('common.connections.pauseUse') : t('common.connections.resumeUse')}
            </button>
            {row.pending ? (
              <button
                type="button"
                className="nb-btn nb-btn--danger-outline"
                disabled={locked}
                onClick={() => {
                  operation.reset();
                  setConfirm({ action: 'withdraw', revision: row.revision });
                }}
              >
                {t('common.connections.withdrawPending')}
              </button>
            ) : null}
            {row.approved?.discovery_source_id || row.pending?.discovery_source_id ? (
              <button
                type="button"
                className="nb-btn nb-btn--secondary"
                disabled={locked}
                onClick={() => run('patch', { discovery_enabled: !row.discovery_enabled })}
              >
                {row.discovery_enabled
                  ? t('common.connections.stopDiscoverySharing')
                  : t('common.connections.resumeDiscoverySharing')}
              </button>
            ) : null}
          </>
        ) : null}
        {row.capabilities.can_reset_failures ? (
          <button
            type="button"
            className="nb-btn nb-btn--secondary"
            disabled={locked}
            onClick={() => {
              operation.reset();
              setConfirm({ action: 'reset', revision: row.revision });
            }}
          >
            {t('common.connections.resetServiceFailures')}
          </button>
        ) : null}
      </div>
      {!confirm ? feedback : null}
      <ConfirmDialog
        open={confirm !== null}
        title={t(
          confirm?.action === 'withdraw'
            ? 'common.connections.withdrawPending'
            : 'common.connections.resetServiceFailures',
        )}
        description={t(
          confirm?.action === 'withdraw'
            ? 'common.connections.withdrawKeepsApproved'
            : 'common.connections.resetServiceOnly',
        )}
        confirmLabel={t('common.connections.confirm')}
        danger={confirm?.action === 'withdraw'}
        busy={operation.isPending}
        confirmDisabled={operation.outcome === 'unknown' || operation.outcome === 'conflict'}
        onCancel={() => setConfirm(null)}
        onConfirm={() => {
          if (confirm)
            void operation
              .mutateAsync({
                action: confirm.action,
                input: { expected_revision: confirm.revision },
              })
              .catch(() => undefined);
        }}
      >
        {feedback}
      </ConfirmDialog>
    </Fold>
  );
}
function VersionMaterial({ label, value }: { label: string; value: UseVersion }) {
  const { t } = useTranslation();
  return (
    <div className="nb-stack nb-stack--sm">
      <h4>{label}</h4>
      <p className="nb-mono">
        {value.display_head}…{value.display_tail} ·{' '}
        {t('common.connections.versionNumber', { version: value.credential_version })}
      </p>
      <p>{t(`common.connections.useStates.${value.status}`)}</p>
      {value.review_note ? <Note>{value.review_note}</Note> : null}
      <p className="nb-sub">
        {value.review.required
          ? t('common.connections.bodyReviewRequired')
          : t('common.connections.bodyReviewNotRequired')}{' '}
        ·{' '}
        {value.review.material_available
          ? t('common.connections.reviewMaterialAvailable')
          : t('common.connections.reviewMaterialUnavailable')}
      </p>
      {value.discovery ? (
        <p className="nb-sub">
          {t('common.connections.sharedDiscoveryState', {
            state: t(`common.connections.sourceStates.${value.discovery.state}`),
            count: value.discovery.count ?? '—',
          })}
        </p>
      ) : null}
    </div>
  );
}
