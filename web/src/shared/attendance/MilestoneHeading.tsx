import { useTranslation } from 'react-i18next';
import { Icon } from '@shared/components/Icon';
import { formatCount } from '@shared/utils/formatNumber';
import './presentation.css';

export function MilestoneHeading({ days, featured }: { days: number; featured?: boolean }) {
  const { t } = useTranslation();
  return (
    <span className="attendance-milestone__heading">
      <strong>{t('attendanceDisplay.day', { amount: formatCount(days).display })}</strong>
      {featured && (
        <span className="attendance-milestone__label">
          <Icon name="rewards" />
          {t('attendanceDisplay.featured')}
        </span>
      )}
    </span>
  );
}
