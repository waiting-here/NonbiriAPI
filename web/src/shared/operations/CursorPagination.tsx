export function CursorPagination({
  page,
  nextCursor,
  onPrevious,
  onNext,
  labels = { previous: 'Previous', next: 'Next', page: 'Page' },
}: {
  page: number;
  nextCursor: string | null | undefined;
  onPrevious: () => void;
  onNext: (cursor: string) => void;
  labels?: { previous: string; next: string; page: string };
}) {
  return (
    <nav className="pagination" aria-label="Pagination">
      <button type="button" className="btn btn-secondary" disabled={page <= 1} onClick={onPrevious}>
        {labels.previous}
      </button>
      <span aria-live="polite">{labels.page} {page}</span>
      <button
        type="button"
        className="btn btn-secondary"
        disabled={!nextCursor}
        onClick={() => nextCursor && onNext(nextCursor)}
      >
        {labels.next}
      </button>
    </nav>
  );
}
