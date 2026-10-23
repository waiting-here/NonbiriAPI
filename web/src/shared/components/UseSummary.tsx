import { StatusBadge } from './States';
import { useTranslation } from 'react-i18next';
import { formatCount } from '@shared/utils/formatNumber';
import { donationDisplayStatus, type UseSummary as Summary } from '@shared/operations/donationUses';
export function UseSummary({ value }: { value?: Summary }) {
  const { t } = useTranslation();
  return value ? (
    <small className="nb-sub">
      {t('common.connections.useSummary', {
        total: formatCount(value.total).display,
        approved: formatCount(value.approved).display,
        pending: formatCount(value.pending).display,
        rejected: formatCount(value.rejected ?? '0').display,
        withdrawn: formatCount(value.withdrawn ?? '0').display,
      })}
    </small>
  ) : null;
}

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
