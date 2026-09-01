import { Link, useParams } from 'react-router';
import { useTranslation } from 'react-i18next';
import { Card, ErrorState, LoadingState, PageHeader, StatusBadge } from '@shared/components/States';
import { formatDateTime } from '@shared/utils/datetime';
import { SafeAnnouncementBody } from '../features/operations/SafeAnnouncementBody';
import { useAnnouncement } from '../features/operations/data';
import '@shared/operations/operations.css';

export function AnnouncementDetailPage() {
  const { announcementId } = useParams();
  const { i18n } = useTranslation();
  const zh = i18n.resolvedLanguage?.toLowerCase().startsWith('zh') ?? false;
  const announcement = useAnnouncement(announcementId);
  if (announcement.isPending) return <LoadingState />;
  if (announcement.error) return <ErrorState error={announcement.error} onRetry={() => void announcement.refetch()} />;
  return <div className="page ops-stack">
    <PageHeader eyebrow="News" title={announcement.data.title} back={<Link to="/announcements">{zh ? '返回公告列表' : 'Back to announcements'}</Link>} />
    <Card className="ops-stack">
      <div className="ops-actions"><StatusBadge active={announcement.data.severity === 'info'} danger={announcement.data.severity === 'important'} label={announcement.data.severity} />
        <span>{formatDateTime(announcement.data.published_at)}</span></div>
      {announcement.data.fallback_from ? <p className="inline-notice">{zh ? `当前语言无版本，已显示 ${announcement.data.effective_language}。` : `No current-language version; showing ${announcement.data.effective_language}.`}</p> : null}
      <SafeAnnouncementBody html={announcement.data.rendered_body} />
    </Card>
  </div>;
}
