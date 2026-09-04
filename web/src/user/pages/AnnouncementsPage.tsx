import { Link } from 'react-router';
import { useTranslation } from 'react-i18next';
import { Card, EmptyState, ErrorState, LoadingState, PageHeader, StatusBadge } from '@shared/components/States';
import { CursorPagination } from '@shared/operations/CursorPagination';
import { useCursorPager } from '@shared/operations/useCursorPager';
import { formatDateTime } from '@shared/utils/datetime';
import { announcementDismissalKey, useAnnouncements, useUserAuthority } from '../features/operations/data';
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

export function AnnouncementsPage() {
  const { t } = useTranslation();
  const pager = useCursorPager();
  const session = useUserAuthority();
  const announcements = useAnnouncements(pager.cursor, !session.error);
  return <div className="page ops-stack">
    <PageHeader eyebrow={t('user.announcements.eyebrow')} title={t('user.announcements.title')} description={t('user.announcements.description')} />
    {session.isPending || announcements.isPending ? <LoadingState /> : session.error ? <ErrorState error={session.error} onRetry={() => void session.refetch()} />
      : announcements.error ? <ErrorState error={announcements.error} onRetry={() => void announcements.refetch()} />
        : announcements.data.data.length === 0 ? <EmptyState title={t('user.announcements.empty')} body={t('user.announcements.emptyBody')} /> : <>
          <div className="ops-grid">
            {announcements.data.data.map((item) => {
              const key = announcementDismissalKey(session.data.id, item);
              const dismissed = typeof localStorage !== 'undefined' && localStorage.getItem(key) === '1';
              return <Card key={item.id} className="ops-stack">
                <div className="card-title-row"><h2>{item.title}</h2><StatusBadge active={item.severity === 'info'} danger={item.severity === 'important'} label={t(SEVERITY_LABEL_KEYS[item.severity])} /></div>
                <p>{item.excerpt}</p>
                <p className="table-note">{formatDateTime(item.published_at)}{item.fallback_from ? ` · ${t('user.announcements.languageFallback', { language: t(LANGUAGE_LABEL_KEYS[item.effective_language]) })}` : ''}</p>
                {dismissed ? <button type="button" className="btn btn-secondary" onClick={() => { localStorage.removeItem(key); void announcements.refetch(); }}>{t('user.announcements.restoreHomeSummary')}</button> : null}
                <Link className="btn btn-secondary" to={`/announcements/${encodeURIComponent(item.id)}`}>{t('user.announcements.readMore')}</Link>
              </Card>;
            })}
          </div>
          <CursorPagination page={pager.page} nextCursor={announcements.data.next_cursor} onPrevious={pager.previous} onNext={pager.next}
            labels={{ previous: t('user.announcements.previous'), next: t('user.announcements.next'), page: t('user.announcements.page') }} />
        </>}
  </div>;
}
