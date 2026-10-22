import { useTranslation } from 'react-i18next';
import { formatCount } from '@shared/utils/formatNumber';
import type { UseSummary as Summary } from '@shared/operations/donationUses';
export function UseSummary({ value }: { value?: Summary }) {
  const { t } = useTranslation();
  return value ? (
    <small className="nb-sub">
      {t('common.connections.useSummary', {
        total: formatCount(value.total).display,
        approved: formatCount(value.approved).display,
        pending: formatCount(value.pending).display,
      })}
    </small>
  ) : null;
}
