import { useTranslation } from 'react-i18next';
import './pagePagination.css';

export function CursorPagination({
  page,
  nextCursor,
  onPrevious,
  onNext,
  labels,
}: {
  page: number;
  nextCursor: string | null | undefined;
  onPrevious: () => void;
  onNext: (cursor: string) => void;
  labels?: { previous: string; next: string; page: string };
}) {
  const { t } = useTranslation();
  const previousLabel = labels?.previous ?? t('common.previous');
  const nextLabel = labels?.next ?? t('common.next');
  const pageLabel = labels ? `${labels.page} ${page}` : t('common.page', { page });

  if (page <= 1 && !nextCursor) return null;
  return (
    <nav className="pagination nb-pager" aria-label={t('common.pagination')}>
      <button
        type="button"
        aria-label={previousLabel}
        className="btn btn-secondary"
        disabled={page <= 1}
        onClick={onPrevious}
      >
        <span aria-hidden="true">‹</span>
      </button>
      <span aria-live="polite">{pageLabel}</span>
      <button
        type="button"
        aria-label={nextLabel}
        className="btn btn-secondary"
        disabled={!nextCursor}
        onClick={() => nextCursor && onNext(nextCursor)}
      >
        <span aria-hidden="true">›</span>
      </button>
    </nav>
  );
}
