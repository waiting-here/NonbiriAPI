import { Link } from 'react-router';
import { useTranslation } from 'react-i18next';
import { Card, EmptyState, ErrorState, LoadingState, PageHeader, StatusBadge } from '@shared/components/States';
import { CursorPagination } from '@shared/operations/CursorPagination';
import { useCursorPager } from '@shared/operations/useCursorPager';
import { formatDateTime } from '@shared/utils/datetime';
import { announcementDismissalKey, useAnnouncements, useUserAuthority } from '../features/operations/data';
import '@shared/operations/operations.css';

export function AnnouncementsPage() {
  const { i18n } = useTranslation();
  const zh = i18n.resolvedLanguage?.toLowerCase().startsWith('zh') ?? false;
  const pager = useCursorPager();
  const session = useUserAuthority();
  const announcements = useAnnouncements(pager.cursor, !session.error);
  return <div className="page ops-stack">
    <PageHeader eyebrow="News" title={zh ? '公告' : 'Announcements'} description={zh ? '置顶公告优先，其余按实际发布时间倒序。' : 'Pinned announcements appear first; the rest follow actual publish time.'} />
    {session.isPending || announcements.isPending ? <LoadingState /> : session.error ? <ErrorState error={session.error} onRetry={() => void session.refetch()} />
      : announcements.error ? <ErrorState error={announcements.error} onRetry={() => void announcements.refetch()} />
        : announcements.data.data.length === 0 ? <EmptyState title={zh ? '暂无公告' : 'No announcements'} body={zh ? '当前没有可见公告。' : 'There are no visible announcements.'} /> : <>
          <div className="ops-grid">
            {announcements.data.data.map((item) => {
              const key = announcementDismissalKey(session.data.id, item);
              const dismissed = typeof localStorage !== 'undefined' && localStorage.getItem(key) === '1';
              return <Card key={item.id} className="ops-stack">
                <div className="card-title-row"><h2>{item.title}</h2><StatusBadge active={item.severity === 'info'} danger={item.severity === 'important'} label={item.severity} /></div>
                <p>{item.excerpt}</p>
                <p className="table-note">{formatDateTime(item.published_at)}{item.fallback_from ? ` · ${zh ? '已回退语言' : 'language fallback'}: ${item.effective_language}` : ''}</p>
                {dismissed ? <button type="button" className="btn btn-secondary" onClick={() => { localStorage.removeItem(key); void announcements.refetch(); }}>{zh ? '恢复首页摘要' : 'Restore home summary'}</button> : null}
                <Link className="btn btn-secondary" to={`/announcements/${encodeURIComponent(item.id)}`}>{zh ? '阅读全文' : 'Read more'}</Link>
              </Card>;
            })}
          </div>
          <CursorPagination page={pager.page} nextCursor={announcements.data.next_cursor} onPrevious={pager.previous} onNext={pager.next}
            labels={{ previous: zh ? '上一页' : 'Previous', next: zh ? '下一页' : 'Next', page: zh ? '页' : 'Page' }} />
        </>}
  </div>;
}
