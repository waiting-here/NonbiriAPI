import { useTranslation } from 'react-i18next';
import { Button } from '@shared/components/ui/Button';
import './pagePagination.css';

export interface SimplePagerProps {
  page: number;
  hasMore: boolean;
  onPrev: () => void;
  onNext: () => void;
  disabled?: boolean;
  labels?: { previous: string; next: string; page?: string };
}

export function SimplePager({ page, hasMore, onPrev, onNext, disabled, labels }: SimplePagerProps) {
  const { t } = useTranslation();
  if (page <= 1 && !hasMore) return null;
  return (
    <nav className="pagination nb-pager" aria-label={t('common.pagination')}>
      <Button
        type="button"
        aria-label={labels?.previous ?? t('common.previous')}
        disabled={disabled || page <= 1}
        onClick={onPrev}
      >
        <span aria-hidden="true">‹</span>
      </Button>
      <span aria-live="polite">{labels?.page ?? t('common.page', { page })}</span>
      <Button
        type="button"
        aria-label={labels?.next ?? t('common.next')}
        disabled={disabled || !hasMore}
        onClick={onNext}
      >
        <span aria-hidden="true">›</span>
      </Button>
    </nav>
  );
}
