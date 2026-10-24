import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  collectPendingDonationUses,
  prepareDonationUseReview,
  runDonationUseReview,
  reviewedUseLabels,
  type DonationUseReviewPlan,
  type ReviewedUseLabel,
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
import { ErrorState, LoadingState } from './States';
import { Field, Fold, Note, OutcomeNote } from './ui';

export function DonationUsesReviewAll({
  role,
  donationId,
  donationKeyId,
  onLockedChange,
  onReviewed,
  refresh,
}: {
  role: 'admin' | 'steward';
  donationId: string;
  donationKeyId?: string;
  onLockedChange: (locked: boolean) => void;
  onReviewed: (results: UseReviewResult[], labels: ReviewedUseLabel[]) => void;
  refresh: () => Promise<void>;
}) {
  const { t } = useTranslation();
  const [plan, setPlan] = useState<DonationUseReviewPlan | null>(null);
  const [note, setNote] = useState('');
  const [results, setResults] = useState<UseReviewResult[]>([]);
  const [checking, setChecking] = useState(false);
  const [exitRefreshError, setExitRefreshError] = useState<unknown>(null);
  const receipts = useRef(new Map<string, UseReviewResult[]>());
  const root = role === 'admin' ? ['admin', 'operations'] : ['user'];
  const prepare = useRetainedOperation<
    { decision: 'approve' | 'reject'; note: string },
    DonationUseReviewPlan
  >(
    async (value, _key, context) => {
      const rows = await collectPendingDonationUses(
        (page) =>
          getDonationUses(role, donationId, page, 100, 'pending', context.signal, donationKeyId),
        context.signal,
      );
      const snapshot = prepareDonationUseReview(rows, value.decision, value.note);
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
        stopped: () => checkOnly && processed,
        onProgress: (value) =>
          context.commit(() => {
            processed = true;
            setResults(value);
            onReviewed(value, reviewedUseLabels(snapshot.rows, snapshot.decision));
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
        onReviewed(value, reviewedUseLabels(snapshot.rows, snapshot.decision));
      });
      await refresh();
      if (error && responseOutcomeUnknown(error)) return { operationConfirmed: true };
    },
    root,
  );
  const incomplete = Boolean(
    plan && results.length < plan.batches.reduce((total, batch) => total + batch.items.length, 0),
  );
  const locked =
    prepare.isPending ||
    review.isPending ||
    checking ||
    incomplete ||
    review.outcome === 'unknown' ||
    Boolean(review.refreshError);
  useEffect(() => {
    onLockedChange(locked);
    return () => onLockedChange(false);
  }, [locked, onLockedChange]);
  const begin = async (decision: 'approve' | 'reject') => {
    setExitRefreshError(null);
    setPlan(null);
    setResults([]);
    receipts.current.clear();
    prepare.reset();
    review.reset();
    try {
      const snapshot = await prepare.mutateAsync({ decision, note });
      if (snapshot.batches.length) await review.mutateAsync(snapshot);
    } catch {
      // The retained operations present errors and keep the original review intent.
    }
  };
  return (
    <div className="nb-stack">
      <Field
        label={t('common.connections.reviewReason')}
        help={t('common.connections.reviewReasonVisible')}
      >
        {(props) => (
          <textarea
            {...props}
            rows={2}
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
          onClick={() => void begin('approve')}
        >
          {t('common.connections.approveAllUses')}
        </button>
        <button
          type="button"
          className="nb-btn nb-btn--danger-outline"
          disabled={locked}
          onClick={() => void begin('reject')}
        >
          {t('common.connections.rejectAllUses')}
        </button>
        {incomplete && !review.isPending && !checking && review.outcome !== 'unknown' ? (
          <button
            type="button"
            className="nb-btn nb-btn--secondary"
            onClick={() => void review.mutateAsync(plan!).catch(() => undefined)}
          >
            {t('common.connections.resumeUseReview')}
          </button>
        ) : null}
      </div>
      {prepare.isPending || review.isPending ? <LoadingState /> : null}
      {prepare.error ? <ErrorState error={prepare.error} /> : null}
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
      {plan ? (
        <div className="nb-stack">
          <Note
            tone={
              incomplete || plan.blocked.length || results.some((entry) => !entry.ok)
                ? 'warn'
                : 'info'
            }
          >
            {plan.rows.length
              ? t('common.connections.reviewProgress', {
                  completed: formatCount(String(results.length)).display,
                  total: formatCount(String(plan.rows.length)).display,
                  succeeded: formatCount(String(results.filter((entry) => entry.ok).length))
                    .display,
                  failed: formatCount(String(results.filter((entry) => !entry.ok).length)).display,
                })
              : t('common.connections.noPendingContent')}
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
          {review.error ? <ErrorState error={review.error} /> : null}
          {review.outcome === 'unknown' ? (
            <Note tone="warn">
              <p>{t('common.connections.unknownResult')}</p>
              <button
                type="button"
                className="nb-btn nb-btn--secondary"
                disabled={review.isPending || checking}
                onClick={() => {
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
            review.isSuccess &&
            !incomplete &&
            !results.some((entry) => !entry.ok) &&
            !plan.blocked.length ? (
              <OutcomeNote
                busy={review.isPending || checking}
                outcome={{ kind: 'savedRefreshFailed', recheck: () => void review.refresh() }}
              />
            ) : (
              <ErrorState
                error={review.refreshError}
                onRetry={review.isSuccess ? () => void review.refresh() : undefined}
              />
            )
          ) : null}
          {incomplete && !review.isPending && !checking && review.outcome !== 'unknown' ? (
            <button
              type="button"
              className="nb-btn nb-btn--secondary"
              onClick={() => {
                setPlan(null);
                receipts.current.clear();
                review.reset();
                void refresh().catch(setExitRefreshError);
              }}
            >
              {t('common.connections.finishUseReview')}
            </button>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}
export function DonationUseReviewResults({
  results,
  labels = [],
  onConfigure,
  configurationStatus,
}: {
  results: UseReviewResult[];
  labels?: ReviewedUseLabel[];
  onConfigure?: (label: ReviewedUseLabel) => void;
  configurationStatus?: (label: ReviewedUseLabel) => 'available' | 'denied' | undefined;
}) {
  const { t } = useTranslation();
  return results.length ? (
    <>
      <ul className="nb-list" role="status">
        {results.map((result) => {
          const label = labels.find((row) => row.use_id === result.use_id);
          return (
            <li key={result.use_id}>
              <span>
                {t(
                  result.ok
                    ? 'common.connections.useReviewSaved'
                    : 'common.connections.useReviewFailed',
                  { id: result.use_id },
                )}
              </span>
              {label ? (
                <span>
                  {' '}
                  · {t(NATIVE_SERVICE_LABEL_KEYS[label.native_service])} · {label.display_head}…
                  {label.display_tail}
                </span>
              ) : null}
              {result.error ? ' · ' + result.error.message : ''}
              {result.ok &&
              label?.decision === 'approve' &&
              onConfigure &&
              configurationStatus?.(label) === 'available' ? (
                <button
                  type="button"
                  className="nb-btn nb-btn--secondary"
                  onClick={() => onConfigure(label)}
                >
                  {t('common.connections.configureReviewedUse')}
                </button>
              ) : null}
            </li>
          );
        })}
      </ul>
      {results.some((result) => {
        const label = labels.find((row) => row.use_id === result.use_id);
        return (
          result.ok && label?.decision === 'approve' && configurationStatus?.(label) === 'denied'
        );
      }) ? (
        <Note>{t('common.connections.reviewConfigurationDenied')}</Note>
      ) : null}
    </>
  ) : null;
}
