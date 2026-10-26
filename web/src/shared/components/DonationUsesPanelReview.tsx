import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  collectPendingDonationUses,
  reviewedUseLabels,
  type ReviewedUseLabel,
} from '@shared/operations/donationUsesReview';
import {
  getDonationUses,
  type DonationUse,
  type UseReviewResult,
} from '@shared/operations/donationUses';
import { reviewPendingDonation, type PendingDonationReviewInput } from '@shared/operations/charity';
import {
  useRetainedOperation,
  type OperationContext,
} from '@shared/operations/useRetainedOperation';
import { responseOutcomeUnknown } from '@shared/operations/api';
import { ErrorState, LoadingState } from './States';
import { Field, Note, OutcomeNote } from './ui';

type ReviewPlan = { rows: DonationUse[]; body: PendingDonationReviewInput };
export function DonationUsesReviewAll({
  role,
  donationId,
  donationRevision,
  onLockedChange,
  onReviewed,
  refresh,
}: {
  role: 'admin' | 'steward';
  donationId: string;
  donationRevision: string;
  onLockedChange: (locked: boolean) => void;
  onReviewed: (results: UseReviewResult[], labels: ReviewedUseLabel[]) => void;
  refresh: () => Promise<void>;
}) {
  const { t } = useTranslation();
  const [note, setNote] = useState('');
  const [empty, setEmpty] = useState(false);
  const root = role === 'admin' ? ['admin', 'operations'] : ['user'];
  const prepare = useRetainedOperation<
    { decision: 'approve' | 'reject'; reason: string },
    ReviewPlan
  >(
    async (value, _key, context) => {
      const rows = await collectPendingDonationUses(
        (page) => getDonationUses(role, donationId, page, 100, 'pending', context.signal),
        context.signal,
      );
      return {
        rows,
        body: {
          expected_revision: donationRevision,
          ...value,
          uses: rows.map((row) => ({
            use_id: row.id,
            expected_revision: row.revision,
            expected_review_revision: row.pending!.expected_review_revision,
          })),
        },
      };
    },
    () => undefined,
    root,
  );
  const send = async (plan: ReviewPlan, key: string, context: OperationContext) => {
    await reviewPendingDonation(role, donationId, plan.body, key, context.signal);
    context.commit(() =>
      onReviewed(
        plan.rows.map((row) => ({ use_id: row.id, ok: true })),
        reviewedUseLabels(plan.rows, plan.body.decision),
      ),
    );
  };
  const review = useRetainedOperation<ReviewPlan, void>(
    send,
    async (plan, error, context) => {
      if (error && responseOutcomeUnknown(error) && context.operationKey)
        await send(plan, context.operationKey, context);
      await refresh();
      if (error && responseOutcomeUnknown(error)) return { operationConfirmed: true };
    },
    root,
  );
  const locked =
    prepare.isPending ||
    review.isPending ||
    review.outcome === 'unknown' ||
    Boolean(review.refreshError);
  useEffect(() => {
    onLockedChange(locked);
    return () => onLockedChange(false);
  }, [locked, onLockedChange]);
  const begin = async (decision: 'approve' | 'reject') => {
    prepare.reset();
    review.reset();
    setEmpty(false);
    try {
      const plan = await prepare.mutateAsync({ decision, reason: note.trim() });
      if (!plan.rows.length) {
        setEmpty(true);
        return;
      }
      await review.mutateAsync(plan);
    } catch {
      /* Retained operations keep the original request for recovery. */
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
      </div>
      {prepare.isPending || review.isPending ? <LoadingState /> : null}
      {empty ? <Note>{t('common.connections.noPendingContent')}</Note> : null}
      {prepare.error ? <ErrorState error={prepare.error} /> : null}
      {review.error ? <ErrorState error={review.error} /> : null}
      {review.outcome === 'unknown' ? (
        <Note tone="warn">
          <p>{t('common.connections.unknownResult')}</p>
          <button
            type="button"
            className="nb-btn nb-btn--secondary"
            disabled={review.isPending}
            onClick={() => void review.check()}
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
          <ErrorState error={review.refreshError} />
        )
      ) : review.isSuccess ? (
        <Note tone="ok">{t('common.connections.reviewSaved')}</Note>
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
  const groups = new Map<string, { label?: ReviewedUseLabel; results: UseReviewResult[] }>();
  for (const result of results) {
    const label = labels.find((row) => row.use_id === result.use_id);
    const key = label?.donation_key_id ?? result.use_id;
    const group = groups.get(key) ?? { label, results: [] };
    group.results.push(result);
    groups.set(key, group);
  }
  return results.length ? (
    <>
      <ul className="nb-list" role="status">
        {[...groups].map(([key, group]) => {
          const { label } = group;
          const expected = label
            ? labels.filter((row) => row.donation_key_id === label.donation_key_id).length
            : group.results.length;
          const saved =
            group.results.length === expected && group.results.every((result) => result.ok);
          return (
            <li key={key}>
              <span>
                {t(
                  saved ? 'common.connections.reviewSaved' : 'common.connections.reviewIncomplete',
                )}
              </span>
              {label ? (
                <span>
                  {' '}
                  · {label.display_head}…{label.display_tail}
                </span>
              ) : null}
              {group.results
                .filter((result) => result.error)
                .map((result) => (
                  <p key={result.use_id}>{result.error!.message}</p>
                ))}
              {group.results.some((result) => result.ok) &&
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
