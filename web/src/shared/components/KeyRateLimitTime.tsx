import { useTranslation } from 'react-i18next';
import { useDateTimeFormatter } from '@shared/utils/datetime';

export function KeyRateLimitTime({ until }: { until: number | null }) {
  const { t } = useTranslation();
  const formatDateTime = useDateTimeFormatter();
  return until === null ? null : (
    <p className="muted">
      {t('common.keyLimits.rateLimitedUntil', { time: formatDateTime(until) })}
    </p>
  );
}
