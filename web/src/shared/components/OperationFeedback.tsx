import { Button } from '@shared/components/ui/Button';
import { useTranslation } from 'react-i18next';
import type { OperationOutcome } from '@shared/operations/useRetainedOperation';

const labels = {
  pending: 'common.operation.pending',
  confirmed: 'common.operation.confirmed',
  unknown: 'common.operation.unknown',
  conflict: 'common.operation.conflict',
  failed: 'common.operation.failed',
  'refresh-failed': 'common.operation.refreshFailed',
} as const;

/** One primary message for the action. Field-specific validation stays beside its field. */
export function OperationFeedback({
  outcome,
  onCheck,
  detail,
  message,
}: {
  outcome: OperationOutcome;
  /** A read-only status/refresh action; never an implicit write retry. */
  onCheck?: () => void;
  detail?: string;
  /** Translated primary message for this specific action, when needed. */
  message?: string;
}) {
  const { t } = useTranslation();
  if (outcome === 'idle') return null;
  const actionable =
    outcome === 'unknown' || outcome === 'conflict' || outcome === 'refresh-failed';
  return (
    <div
      className={`nb-operation-feedback nb-operation-feedback--${outcome}`}
      role={outcome === 'failed' || outcome === 'conflict' ? 'alert' : 'status'}
      aria-live="polite"
    >
      <p>{message ?? t(labels[outcome])}</p>
      {detail ? (
        <details>
          <summary>{t('common.operation.details')}</summary>
          <pre>{detail}</pre>
        </details>
      ) : null}
      {actionable && onCheck ? (
        <Button type="button"  onClick={onCheck}>
          {t('common.operation.check')}
        </Button>
      ) : null}
    </div>
  );
}
