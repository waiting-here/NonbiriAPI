import { StatusBadge } from './ui';
import { useTranslation } from 'react-i18next';
import { donationDisplayStatus, type UseSummary as Summary } from '@shared/operations/donationUses';

export function DonationStatusBadge({
  status,
  summary,
}: {
  status: Parameters<typeof donationDisplayStatus>[0];
  summary?: Summary;
}) {
  const { t } = useTranslation();
  const display = donationDisplayStatus(status, summary);
  const tone =
    display === 'approved'
      ? 'ok'
      : display === 'pending' ||
          display === 'approved_pending' ||
          display === 'partially_approved' ||
          display === 'mixed'
        ? 'warn'
        : display === 'rejected'
          ? 'bad'
          : 'neutral';
  return <StatusBadge tone={tone}>{t(`common.connections.donationStates.${display}`)}</StatusBadge>;
}
