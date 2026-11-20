import { Link, useLocation, useParams } from 'react-router';
import { listReturnPath } from '@shared/operations/listReturn';
import { useTranslation } from 'react-i18next';
import { Card, ErrorState, LoadingState, PageHeader } from '@shared/components/States';
import { StatusBadge } from '@shared/components/ui';
import { formatDateTime } from '@shared/utils/datetime';
import { SafeAnnouncementBody } from '../features/operations/SafeAnnouncementBody';
import { useAnnouncement } from '../features/operations/data';
import '@shared/operations/operations.css';

const SEVERITY_TONES = {
  info: 'info',
  warning: 'warn',
  important: 'bad',
} as const;

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
  const location = useLocation();
  const fromHome = location.state?.returnTo === '/';
  const backTo = fromHome ? '/' : listReturnPath(location.state, '/announcements');
  const { t } = useTranslation();
  const announcement = useAnnouncement(announcementId);
  return (
    <div className="page ops-stack ops-readable-page">
      <PageHeader
        eyebrow={announcement.data ? t('user.announcements.eyebrow') : undefined}
        title={announcement.data?.title ?? t('user.announcements.title')}
        back={
          <Link to={backTo} state={{ announcementSource: location.state?.announcementSource }}>
            {t(fromHome ? 'common.backHome' : 'user.announcements.backToList')}
          </Link>
        }
      />
      {announcement.isPending ? (
        <LoadingState />
      ) : announcement.error ? (
        <ErrorState error={announcement.error} onRetry={() => void announcement.refetch()} />
      ) : (
        <Card className="ops-stack">
          <div className="announcement-detail__meta">
            <StatusBadge tone={SEVERITY_TONES[announcement.data.severity]}>
              {t(SEVERITY_LABEL_KEYS[announcement.data.severity])}
            </StatusBadge>
            {announcement.data.pinned ? (
              <StatusBadge tone="accent">{t('user.core.home.announcement.pinned')}</StatusBadge>
            ) : null}
            <span className="nb-small nb-muted">
              {formatDateTime(announcement.data.published_at)}
            </span>
          </div>
          {announcement.data.fallback_from ? (
            <p className="inline-notice">
              {t('user.announcements.fallbackNotice', {
                language: t(LANGUAGE_LABEL_KEYS[announcement.data.effective_language]),
              })}
            </p>
          ) : null}
          <SafeAnnouncementBody html={announcement.data.rendered_body} />
        </Card>
      )}
    </div>
  );
}
