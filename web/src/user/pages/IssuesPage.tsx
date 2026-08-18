import { useState } from 'react';
import { formatDateTime } from '@shared/utils/datetime';
import { useTranslation } from 'react-i18next';
import {
  Card,
  EmptyState,
  ErrorState,
  LoadingState,
  PageHeader,
  Pagination,
  ReadOnlyValue,
  StatusBadge,
} from '@shared/components/States';
import { useResolveUserIssue, useUserIssues } from '../data';
import { UserPageGate } from '../components/UserPageGate';

const PAGE_SIZE_OPTIONS = [10, 20, 50] as const;

function IssuesContent() {
  const { t } = useTranslation();
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState<number>(PAGE_SIZE_OPTIONS[1]);
  const [cursors, setCursors] = useState<Record<number, string>>({});
  const [draftResolved, setDraftResolved] = useState<'all' | 'unresolved' | 'resolved'>('all');
  const [resolvedFilter, setResolvedFilter] = useState<boolean | undefined>(undefined);
  const beforeId = page > 1 ? cursors[page - 1] : undefined;
  const issues = useUserIssues(resolvedFilter, beforeId, pageSize);
  const resolve = useResolveUserIssue();

  const changePage = (nextPage: number) => {
    if (nextPage > page) {
      const nextCursor = issues.data?.nextCursor;
      if (!nextCursor) return;
      setCursors((current) => ({ ...current, [page]: nextCursor }));
    }
    setPage(nextPage);
  };

  const changePageSize = (size: number) => {
    setPageSize(size);
    setPage(1);
    setCursors({});
  };

  return (
    <div className="page">
      <PageHeader
        eyebrow={t('app.name')}
        title={t('user.issues.title')}
        description={t('user.issues.description')}
      />
      {resolve.error ? <ErrorState error={resolve.error} /> : null}
      <Card>
        <div className="card-title-row">
          <h2>{t('user.issues.listTitle')}</h2>
          <span className="muted">{t('common.page', { page })}</span>
        </div>
        <form
          className="filter-bar"
          onSubmit={(event) => {
            event.preventDefault();
            setResolvedFilter(
              draftResolved === 'all' ? undefined : draftResolved === 'resolved',
            );
            setPage(1);
            setCursors({});
          }}
        >
          <label>
            <span>{t('user.issues.filterResolved')}</span>
            <select
              value={draftResolved}
              onChange={(event) => setDraftResolved(event.target.value as 'all' | 'unresolved' | 'resolved')}
              aria-label={t('common.filterResolvedAria')}
            >
              <option value="all">{t('common.all')}</option>
              <option value="unresolved">{t('common.unresolved')}</option>
              <option value="resolved">{t('common.resolved')}</option>
            </select>
          </label>
          <div className="filter-actions">
            <button type="submit" className="btn btn-quiet">
              {t('common.applyFilter')}
            </button>
            <button
              type="button"
              className="btn btn-link"
              onClick={() => {
                setDraftResolved('all');
                setResolvedFilter(undefined);
                setPage(1);
                setCursors({});
              }}
            >
              {t('common.resetFilter')}
            </button>
          </div>
        </form>
        {issues.isPending ? (
          <LoadingState />
        ) : issues.error ? (
          <ErrorState error={issues.error} onRetry={() => void issues.refetch()} />
        ) : issues.data.items.length === 0 ? (
          <EmptyState title={t('user.issues.empty')} body={t('user.issues.emptyBody')} />
        ) : (
          <>
            <div className="item-list">
              {issues.data.items.map((issue) => (
                <article className="item-card" key={issue.id}>
                  <div className="item-header">
                    <div>
                      <h2>{issue.kind}</h2>
                      <p className="item-meta">{formatDateTime(issue.created_at)}</p>
                    </div>
                    <StatusBadge
                      active={issue.resolved}
                      label={issue.resolved ? t('user.issues.resolved') : t('user.issues.open')}
                    />
                  </div>
                  <p className="item-note">{issue.message || t('common.notAvailable')}</p>
                  {issue.ref ? (
                    <p className="item-meta">
                      {t('user.issues.reference')}: <ReadOnlyValue value={issue.ref} />
                    </p>
                  ) : null}
                  {issue.resolved ? (
                    <p className="item-meta">{t('user.issues.resolvedValue')}</p>
                  ) : (
                    <div className="form-actions">
                      <button
                        type="button"
                        className="btn btn-secondary"
                        disabled={resolve.isPending}
                        onClick={() => resolve.mutate(issue.id)}
                      >
                        {resolve.isPending ? t('common.working') : t('user.issues.resolve')}
                      </button>
                    </div>
                  )}
                </article>
              ))}
            </div>
            <Pagination
              page={page}
              hasNext={issues.data.hasMore}
              onChange={changePage}
              pageSize={pageSize}
              pageSizeOptions={PAGE_SIZE_OPTIONS}
              onPageSizeChange={changePageSize}
            />
          </>
        )}
      </Card>
    </div>
  );
}

export function IssuesPage() {
  return (
    <UserPageGate>
      <IssuesContent />
    </UserPageGate>
  );
}
