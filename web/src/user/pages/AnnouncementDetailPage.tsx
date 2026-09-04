import { Link, useParams } from 'react-router';
import { useTranslation } from 'react-i18next';
import { Card, ErrorState, LoadingState, PageHeader, StatusBadge } from '@shared/components/States';
import { formatDateTime } from '@shared/utils/datetime';
import { SafeAnnouncementBody } from '../features/operations/SafeAnnouncementBody';
import { useAnnouncement } from '../features/operations/data';
import '@shared/operations/operations.css';

const SEVERITY_LABEL_KEYS = {
  info: 'user.announcements.severity.info',
  warning: 'user.announcements.severity.warning',
  important: 'user.announcements.severity.important',
} as const;

const LANGUAGE_LABEL_KEYS = {
  zh: 'user.announcements.language.zh',
  en: 'user.announcements.language.en',
} as const;

export function AnnouncementDetailPage() {
  const { announcementId } = useParams();
  const { t } = useTranslation();
  const announcement = useAnnouncement(announcementId);
  if (announcement.isPending) return <LoadingState />;
  if (announcement.error) return <ErrorState error={announcement.error} onRetry={() => void announcement.refetch()} />;
  return <div className="page ops-stack">
    <PageHeader eyebrow={t('user.announcements.eyebrow')} title={announcement.data.title} back={<Link to="/announcements">{t('user.announcements.backToList')}</Link>} />
    <Card className="ops-stack">
      <div className="ops-actions"><StatusBadge active={announcement.data.severity === 'info'} danger={announcement.data.severity === 'important'} label={t(SEVERITY_LABEL_KEYS[announcement.data.severity])} />
        <span>{formatDateTime(announcement.data.published_at)}</span></div>
      {announcement.data.fallback_from ? <p className="inline-notice">{t('user.announcements.fallbackNotice', { language: t(LANGUAGE_LABEL_KEYS[announcement.data.effective_language]) })}</p> : null}
      <SafeAnnouncementBody html={announcement.data.rendered_body} />
    </Card>
  </div>;
}
