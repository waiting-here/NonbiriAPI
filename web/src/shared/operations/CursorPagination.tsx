import { useTranslation } from 'react-i18next';
import { SimplePager } from './SimplePager';

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
  return (
    <SimplePager
      page={page}
      hasMore={Boolean(nextCursor)}
      onPrev={onPrevious}
      onNext={() => {
        if (nextCursor) onNext(nextCursor);
      }}
      labels={{
        previous: labels?.previous ?? t('common.previous'),
        next: labels?.next ?? t('common.next'),
        page: labels ? `${labels.page} ${page}` : t('common.page', { page }),
      }}
    />
  );
}
