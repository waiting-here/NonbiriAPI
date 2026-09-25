import { useEffect } from 'react';
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
}: {
  donationID: string;
  role: CharityRole;
  handling: DonationHandling;
  refresh: () => Promise<unknown>;
  onCapabilityLoss?: () => void;
}) {
  const formatDateTime = useDateTimeFormatter();
  const { t } = useTranslation();
  const process = useRetainedOperation<{ revision: string }, DonationHandlingReceipt>(
    (input, key) => processManagedDonation(role, donationID, input.revision, key),
    refresh,
    charityKeys.root(role),
  );
  const lost = isUnauthorized(process.error) || isForbidden(process.error);
  useEffect(() => {
    if (lost) onCapabilityLoss?.();
  }, [lost, onCapabilityLoss]);
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
          <p>{t('common.donationHandling.conflictHint')}</p>
          <button type="button" className="btn btn-secondary" onClick={() => void refresh()}>
            {t('common.refresh')}
          </button>
        </>
      ) : null}
      {handling.state === 'pending' ? (
        <button
          type="button"
          className="btn btn-primary"
          disabled={process.isPending}
          onClick={() => process.mutate({ revision: handling.revision })}
        >
          {t('common.donationHandling.process')}
        </button>
      ) : null}
    </section>
  );
}
