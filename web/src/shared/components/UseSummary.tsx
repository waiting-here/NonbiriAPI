import { StatusBadge } from './States';
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
  return (
    <StatusBadge
      active={display === 'approved' || display === 'partially_approved'}
      danger={display === 'rejected' || display === 'deleted' || display === 'expired'}
      label={t(`common.connections.donationStates.${display}`)}
    />
  );
}
