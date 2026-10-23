import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  collectPendingDonationUses,
  prepareDonationUseReview,
  runDonationUseReview,
  type DonationUseReviewPlan,
} from '@shared/operations/donationUsesReview';
import {
  getDonationUses,
  reviewDonationUses,
  type UseReviewResult,
} from '@shared/operations/donationUses';
import {
  useRetainedOperation,
  type OperationContext,
} from '@shared/operations/useRetainedOperation';
import { responseOutcomeUnknown } from '@shared/operations/api';
import { NATIVE_SERVICE_LABEL_KEYS } from '@shared/nativeServices';
import { formatCount } from '@shared/utils/formatNumber';
import { ConfirmDialog } from './ConfirmDialog';
import { ErrorState, LoadingState } from './States';
import { Field, Fold, Note, OutcomeNote } from './ui';

export function DonationUsesReviewAll({
  role,
  donationId,
  donationKeyId,
  disabled,
  onLockedChange,
  onReviewed,
  refresh,
}: {
  role: 'admin' | 'steward';
  donationId: string;
  donationKeyId?: string;
  disabled: boolean;
  onLockedChange: (locked: boolean) => void;
  onReviewed: (results: UseReviewResult[]) => void;
  refresh: () => Promise<void>;
}) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [decision, setDecision] = useState<'approve' | 'reject'>('approve');
  const [plan, setPlan] = useState<DonationUseReviewPlan | null>(null);
  const [note, setNote] = useState('');
  const [started, setStarted] = useState(false);
  const [results, setResults] = useState<UseReviewResult[]>([]);
  const [stopping, setStopping] = useState(false);
  const [checking, setChecking] = useState(false);
  const [exitRefreshError, setExitRefreshError] = useState<unknown>(null);
  const receipts = useRef(new Map<string, UseReviewResult[]>());
  const stop = useRef(false);
  const root = role === 'admin' ? ['admin', 'operations'] : ['user'];
  const prepare = useRetainedOperation<'approve' | 'reject', DonationUseReviewPlan>(
    async (value, _key, context) => {
      const rows = await collectPendingDonationUses(
        (page) =>
          getDonationUses(role, donationId, page, 100, 'pending', context.signal, donationKeyId),
        context.signal,
      );
      const snapshot = prepareDonationUseReview(rows, value, '');
      context.commit(() => setPlan(snapshot));
      return snapshot;
    },
    () => undefined,
    root,
  );
  const run = (snapshot: DonationUseReviewPlan, context: OperationContext, checkOnly = false) => {
    let processed = false;
    return runDonationUseReview(
      snapshot,
      receipts.current,
      (items, key) => reviewDonationUses(role, donationId, items, key, context.signal),
      {
        assertCurrent: context.assertCurrent,
        stopped: () => (checkOnly ? processed : stop.current),
        onProgress: (value) =>
          context.commit(() => {
            processed = true;
            setResults(value);
          }),
      },
    );
  };
  const review = useRetainedOperation<DonationUseReviewPlan, UseReviewResult[]>(
    async (snapshot, _key, context) => run(snapshot, context),
    async (snapshot, error, context) => {
      if (error && responseOutcomeUnknown(error)) await run(snapshot, context, true);
      const value = snapshot.batches.flatMap((batch) => receipts.current.get(batch.key) ?? []);
      context.commit(() => {
        setResults(value);
        onReviewed(value);
      });
      await refresh();
      if (error && responseOutcomeUnknown(error)) return { operationConfirmed: true };
    },
    root,
  );
  const incomplete = Boolean(
    started &&
    plan &&
    results.length < plan.batches.reduce((total, batch) => total + batch.items.length, 0),
  );
  const locked =
    open ||
    prepare.isPending ||
    review.isPending ||
    checking ||
    incomplete ||
    review.outcome === 'unknown';
  useEffect(() => {
    onLockedChange(locked);
    return () => onLockedChange(false);
  }, [locked, onLockedChange]);
  const begin = (value: 'approve' | 'reject') => {
    setExitRefreshError(null);
    setDecision(value);
    setPlan(null);
    setNote('');
    setStarted(false);
    setResults([]);
    setStopping(false);
    receipts.current.clear();
    stop.current = false;
    prepare.reset();
    review.reset();
    setOpen(true);
    void prepare.mutateAsync(value).catch(() => undefined);
  };
  const execute = () => {
    if (!plan) return;
    const snapshot = started ? plan : prepareDonationUseReview(plan.rows, decision, note);
    setPlan(snapshot);
    setStarted(true);
    setStopping(false);
    stop.current = false;
    void review.mutateAsync(snapshot).catch(() => undefined);
  };
  const eligible = plan ? plan.rows.length - plan.blocked.length : 0;
  const title = t(
    decision === 'approve'
      ? 'common.connections.approveAllUses'
      : 'common.connections.rejectAllUses',
  );
  return (
    <>
      <div className="nb-actions">
        <button
          type="button"
          className="nb-btn nb-btn--secondary"
          disabled={disabled || locked}
          onClick={() => begin('approve')}
        >
          {t('common.connections.approveAllUses')}
        </button>
        <button
          type="button"
          className="nb-btn nb-btn--danger-outline"
          disabled={disabled || locked}
          onClick={() => begin('reject')}
        >
          {t('common.connections.rejectAllUses')}
        </button>
        {!open && (incomplete || review.outcome === 'unknown') ? (
          <button type="button" className="nb-btn nb-btn--secondary" onClick={() => setOpen(true)}>
            {t('common.connections.resumeUseReview')}
          </button>
        ) : null}
      </div>
      {exitRefreshError ? (
        <ErrorState
          error={exitRefreshError}
          onRetry={() =>
            void refresh()
              .then(() => setExitRefreshError(null))
              .catch(setExitRefreshError)
          }
        />
      ) : null}
      <ConfirmDialog
        open={open}
        title={title}
        description={t(
          donationKeyId
            ? 'common.connections.reviewKeyScope'
            : 'common.connections.reviewDonationScope',
          { donation: donationId, key: donationKeyId },
        )}
        confirmLabel={started ? t('common.connections.resumeUseReview') : title}
        cancelLabel={started ? t('common.close') : undefined}
        danger={decision === 'reject'}
        busy={prepare.isPending || review.isPending || checking}
        confirmDisabled={
          !plan || eligible === 0 || review.outcome === 'unknown' || (started && !incomplete)
        }
        onCancel={() => setOpen(false)}
        onConfirm={execute}
      >
        {prepare.isPending ? (
          <>
            <LoadingState />
            <button
              type="button"
              className="nb-btn nb-btn--secondary"
              onClick={() => {
                prepare.cancel();
                setOpen(false);
              }}
            >
              {t('common.connections.cancelUseReviewLoading')}
            </button>
          </>
        ) : null}
        {prepare.error ? (
          <ErrorState
            error={prepare.error}
            onRetry={() => void prepare.mutateAsync(decision).catch(() => undefined)}
          />
        ) : null}
        {plan ? (
          <div className="nb-stack">
            <Note tone={plan.blocked.length ? 'warn' : 'info'}>
              <p>
                {t('common.connections.reviewSnapshotCount', {
                  total: formatCount(String(plan.rows.length)).display,
                  eligible: formatCount(String(eligible)).display,
                  blocked: formatCount(String(plan.blocked.length)).display,
                })}
              </p>
              <p>{t('common.connections.reviewSnapshotFixed')}</p>
              {decision === 'reject' ? (
                <p>{t('common.connections.withdrawKeepsApproved')}</p>
              ) : null}
            </Note>
            {plan.blocked.length ? (
              <Fold title={t('common.connections.blockedUses')}>
                <ul className="nb-list">
                  {plan.blocked.map((row) => (
                    <li key={row.id}>
                      {t('common.connections.reviewUseLabel', {
                        key: row.donation_key_id,
                        service: t(NATIVE_SERVICE_LABEL_KEYS[row.native_service]),
                      })}
                      {' · '}
                      {t(
                        row.pending?.review.material_available
                          ? 'common.connections.useReviewUnavailable'
                          : 'common.connections.reviewMaterialUnavailable',
                      )}
                    </li>
                  ))}
                </ul>
              </Fold>
            ) : null}
            {!started ? (
              <Field
                label={t('common.connections.reviewReason')}
                help={t('common.connections.reviewReasonVisible')}
              >
                {(props) => (
                  <textarea
                    {...props}
                    value={note}
                    onChange={(event) => setNote(event.target.value)}
                  />
                )}
              </Field>
            ) : null}
            {started ? (
              <p role="status">
                {t('common.connections.reviewProgress', {
                  completed: formatCount(String(results.length)).display,
                  total: formatCount(String(eligible)).display,
                  succeeded: formatCount(String(results.filter((entry) => entry.ok).length))
                    .display,
                  failed: formatCount(String(results.filter((entry) => !entry.ok).length)).display,
                })}
              </p>
            ) : null}
            {review.isPending ? (
              <button
                type="button"
                className="nb-btn nb-btn--secondary"
                disabled={stopping}
                onClick={() => {
                  stop.current = true;
                  setStopping(true);
                }}
              >
                {t(
                  stopping
                    ? 'common.connections.stoppingUseReview'
                    : 'common.connections.stopUseReview',
                )}
              </button>
            ) : null}
            {review.error ? <ErrorState error={review.error} /> : null}
            {review.outcome === 'unknown' ? (
              <Note tone="warn">
                <p>{t('common.connections.unknownResult')}</p>
                <button
                  type="button"
                  className="nb-btn nb-btn--secondary"
                  disabled={review.isPending || checking}
                  onClick={() => {
                    stop.current = false;
                    setChecking(true);
                    void review
                      .check()
                      .catch(() => undefined)
                      .finally(() => setChecking(false));
                  }}
                >
                  {t('common.connections.checkResult')}
                </button>
              </Note>
            ) : null}
            {review.refreshError ? (
              review.isSuccess ? (
                <OutcomeNote
                  busy={review.isPending || checking}
                  outcome={{ kind: 'savedRefreshFailed', recheck: () => void review.refresh() }}
                />
              ) : (
                <ErrorState error={review.refreshError} />
              )
            ) : null}
            {incomplete && !review.isPending && !checking && review.outcome !== 'unknown' ? (
              <button
                type="button"
                className="nb-btn nb-btn--secondary"
                onClick={() => {
                  stop.current = true;
                  onReviewed(results);
                  setPlan(null);
                  setStarted(false);
                  setOpen(false);
                  receipts.current.clear();
                  review.reset();
                  void refresh().catch(setExitRefreshError);
                }}
              >
                {t('common.connections.finishUseReview')}
              </button>
            ) : null}
            {results.length ? (
              <Fold title={t('common.connections.reviewResults')}>
                <DonationUseReviewResults results={results} />
              </Fold>
            ) : null}
          </div>
        ) : null}
      </ConfirmDialog>
    </>
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
