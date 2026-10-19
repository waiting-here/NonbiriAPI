import { Button } from '@shared/components/ui/Button';
import { useEffect, useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { useRetainedOperation } from '../../admin/features/operations/useRetainedOperation';
import {
  charityKeys,
  processManagedDonation,
  type CharityRole,
  type DonationHandling,
  type DonationHandlingReceipt,
} from '@shared/operations/charity';
import { isForbidden, isUnauthorized } from '@shared/query/http';
import { useDateTimeFormatter } from '@shared/utils/datetime';
import { ErrorState, StatusBadge } from './States';
import {
  donationHandlingStateKey,
  donationHandlingHelpKey,
  donationHandlingRoleKey,
  donationHandlingReasonKey,
} from './donationHandlingCopy';

export function DonationHandlingStatus({ handling }: { handling: DonationHandling }) {
  const { t } = useTranslation();
  return (
    <StatusBadge
      active={handling.state === 'pending'}
      label={t(donationHandlingStateKey[handling.state])}
    />
  );
}

export function DonationHandlingControl({
  donationID,
  role,
  handling,
  refresh,
  onCapabilityLoss,
  keyCount,
  disabled = false,
}: {
  donationID: string;
  role: CharityRole;
  handling: DonationHandling;
  refresh: () => Promise<unknown>;
  onCapabilityLoss?: () => void;
  keyCount?: string;
  disabled?: boolean;
}) {
  const formatDateTime = useDateTimeFormatter();
  const client = useQueryClient();
  const { t } = useTranslation();
  const [reading, setReading] = useState(false);
  const [readError, setReadError] = useState<unknown>(null);
  const process = useRetainedOperation<{ revision: string }, DonationHandlingReceipt>(
    (input, key) => processManagedDonation(role, donationID, input.revision, key),
    refresh,
    charityKeys.root(role),
  );
  const lost = isUnauthorized(process.error) || isForbidden(process.error);
  const reread = async () => {
    setReading(true);
    setReadError(null);
    try {
      await refresh();
      process.reset();
    } catch (error) {
      setReadError(error);
    } finally {
      setReading(false);
    }
  };
  useEffect(() => {
    if (!lost) return;
    void client.cancelQueries({ queryKey: charityKeys.root(role) });
    client.removeQueries({ queryKey: charityKeys.root(role) });
    onCapabilityLoss?.();
  }, [client, lost, onCapabilityLoss, role]);
  if (lost)
    return (
      <p className="field-error" role="alert">
        {t('common.operations.charity.accessLost')}
      </p>
    );
  return (
    <section className="ops-subcard" aria-label={t('common.donationHandling.title')}>
      <h4>{t('common.donationHandling.title')}</h4>
      <DonationHandlingStatus handling={handling} />
      <p>{t(donationHandlingHelpKey[handling.state])}</p>
      {keyCount ? <p>{t('common.workflow.wholeDonation', { count: keyCount })}</p> : null}
      {handling.processed_at !== null && handling.processed_by_role !== null ? (
        <p>
          {t('common.donationHandling.processedInfo', {
            at: formatDateTime(handling.processed_at),
            role: t(donationHandlingRoleKey[handling.processed_by_role]),
          })}
        </p>
      ) : null}
      {handling.closed_at !== null && handling.closed_reason !== null ? (
        <p>
          {t('common.donationHandling.closedInfo', {
            at: formatDateTime(handling.closed_at),
            reason: t(donationHandlingReasonKey[handling.closed_reason]),
          })}
        </p>
      ) : null}
      {process.error ? (
        <>
          <ErrorState error={process.error} />
          {process.outcome === 'conflict' ? (
            <>
              <p>{t('common.donationHandling.conflictHint')}</p>
              <Button type="button" disabled={reading} onClick={() => void reread()}>
                {t('common.refresh')}
              </Button>
            </>
          ) : null}
        </>
      ) : null}
      {readError ? <ErrorState error={readError} onRetry={() => void reread()} /> : null}
      {handling.state === 'pending' ? (
        <Button
          type="button"
          variant="primary"
          disabled={
            disabled ||
            reading ||
            process.isPending ||
            process.outcome === 'unknown' ||
            process.outcome === 'conflict' ||
            process.outcome === 'refresh-failed'
          }
          onClick={() => process.mutate({ revision: handling.revision })}
        >
          {t(keyCount ? 'common.workflow.processWhole' : 'common.donationHandling.process')}
        </Button>
      ) : null}
      {process.outcome === 'unknown' && process.variables ? (
        <Button
          type="button"
          disabled={process.isPending}
          onClick={() => process.mutate(process.variables!)}
        >
          {t('common.donationReview.retrySame')}
        </Button>
      ) : null}
      {process.refreshError ? (
        <ErrorState
          error={process.refreshError}
          onRetry={() => {
            if (process.outcome === 'refresh-failed') void process.refresh();
            else void reread();
          }}
        />
      ) : null}
    </section>
  );
}
