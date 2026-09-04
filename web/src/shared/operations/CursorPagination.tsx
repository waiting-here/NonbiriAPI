import { useTranslation } from 'react-i18next';

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

  return (
    <nav className="pagination" aria-label={t('common.pagination')}>
      <button type="button" className="btn btn-secondary" disabled={page <= 1} onClick={onPrevious}>
        {previousLabel}
      </button>
      <span aria-live="polite">{pageLabel}</span>
      <button
        type="button"
        className="btn btn-secondary"
        disabled={!nextCursor}
        onClick={() => nextCursor && onNext(nextCursor)}
      >
        {nextLabel}
      </button>
    </nav>
  );
}
