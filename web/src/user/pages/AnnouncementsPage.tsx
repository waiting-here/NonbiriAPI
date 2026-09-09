import { Link, useLocation } from 'react-router';
import { useTranslation } from 'react-i18next';
import {
  Card,
  EmptyState,
  ErrorState,
  LoadingState,
  PageHeader,
  StatusBadge,
} from '@shared/components/States';
import { PagePagination } from '@shared/operations/PagePagination';
import { useUrlPagePager } from '@shared/operations/useUrlPagePager';
import { formatDateTime } from '@shared/utils/datetime';
import {
  announcementDismissalKey,
  useAnnouncementsPage,
  useUserAuthority,
} from '../features/operations/data';
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

function isAnnouncementDismissed(key: string): boolean {
  if (typeof localStorage === 'undefined') return false;
  try {
    return localStorage.getItem(key) === '1';
  } catch {
    return false;
  }
}

function restoreAnnouncement(key: string): void {
  if (typeof localStorage !== 'undefined') {
    try {
      localStorage.removeItem(key);
    } catch {
      // A disabled store must not prevent the list from being read.
    }
  }
}

export function AnnouncementsPage() {
  const { t } = useTranslation();
  const location = useLocation();
  const session = useUserAuthority();
  const accountID = session.data?.id;
  const pager = useUrlPagePager({
    station: 'user',
    listType: 'announcements',
    scopeKey: accountID ?? 'anonymous',
    scopeReady: Boolean(accountID) && !session.error,
  });
  const announcements = useAnnouncementsPage(
    accountID,
    pager.page,
    pager.pageSize,
    !session.error,
    session.data?.lang,
  );
  const pageData = announcements.data;
  const busy = announcements.isFetching;
  const returnParams = new URLSearchParams(location.search);
  returnParams.set('page', pageData?.pagination.page ?? pager.page);
  returnParams.set('page_size', String(pager.pageSize));
  const returnTo = `/announcements?${returnParams.toString()}`;
  return (
    <div className="page ops-stack">
      <PageHeader
        eyebrow={t('user.announcements.eyebrow')}
        title={t('user.announcements.title')}
        description={t('user.announcements.description')}
      />
      {session.error ? (
        <ErrorState error={session.error} onRetry={() => void session.refetch()} />
      ) : session.isPending || announcements.isPending ? (
        <LoadingState />
      ) : announcements.error ? (
        <ErrorState error={announcements.error} onRetry={() => void announcements.refetch()} />
      ) : pageData ? (
        <>
          {pageData.data.length === 0 ? (
            <EmptyState
              title={t('user.announcements.empty')}
              body={t('user.announcements.emptyBody')}
            />
          ) : (
            <div className="ops-grid" aria-busy={busy}>
              {pageData.data.map((item) => {
                const key = announcementDismissalKey(session.data.id, item);
                const dismissed = isAnnouncementDismissed(key);
                return (
                  <Card key={item.id} className="ops-stack">
                    <div className="card-title-row">
                      <h2>{item.title}</h2>
                      <StatusBadge
                        active={item.severity === 'info'}
                        danger={item.severity === 'important'}
                        label={t(SEVERITY_LABEL_KEYS[item.severity])}
                      />
                    </div>
                    <p>{item.excerpt}</p>
                    <p className="table-note">
                      {formatDateTime(item.published_at)}
                      {item.fallback_from
                        ? ` · ${t('user.announcements.languageFallback', { language: t(LANGUAGE_LABEL_KEYS[item.effective_language]) })}`
                        : ''}
                    </p>
                    {dismissed ? (
                      <button
                        type="button"
                        className="btn btn-secondary"
                        onClick={() => {
                          restoreAnnouncement(key);
                          void announcements.refetch();
                        }}
                      >
                        {t('user.announcements.restoreHomeSummary')}
                      </button>
                    ) : null}
                    <Link
                      className="btn btn-secondary"
                      to={`/announcements/${encodeURIComponent(item.id)}`}
                      state={{ returnTo }}
                    >
                      {t('user.announcements.readMore')}
                    </Link>
                  </Card>
                );
              })}
            </div>
          )}
          <PagePagination
            metadata={pageData.pagination}
            requestedPage={pager.page}
            busy={busy}
            onPageChange={pager.setPage}
            onPageSizeChange={pager.setPageSize}
          />
        </>
      ) : (
        <LoadingState />
      )}
    </div>
  );
}
