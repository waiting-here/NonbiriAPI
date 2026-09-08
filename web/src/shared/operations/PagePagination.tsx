import { useId, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  isPageNumber,
  MAX_PAGE,
  PAGE_SIZES,
  type PageMetadata,
  type PageSize,
} from './pageNumbers';
import './pagePagination.css';

interface PagePaginationProps {
  metadata: PageMetadata;
  onPageChange: (page: string) => void;
  onPageSizeChange: (size: PageSize) => void;
  busy?: boolean;
}

export function PagePagination(props: PagePaginationProps) {
  return <PagePaginationControls key={props.metadata.page} {...props} />;
}

function PagePaginationControls({
  metadata,
  onPageChange,
  onPageSizeChange,
  busy = false,
}: PagePaginationProps) {
  const { t } = useTranslation();
  const errorId = useId();
  const [jump, setJump] = useState(metadata.page);
  const [invalid, setInvalid] = useState(false);
  const current = BigInt(metadata.page);
  const last = BigInt(metadata.total_pages) > MAX_PAGE ? MAX_PAGE : BigInt(metadata.total_pages);
  const go = () => {
    if (busy) return;
    if (!isPageNumber(jump)) {
      setInvalid(true);
      return;
    }
    const selected = BigInt(jump) > last ? last.toString() : jump;
    setJump(selected);
    setInvalid(false);
    onPageChange(selected);
  };
  return (
    <nav className="pagination page-pagination" aria-label={t('common.pagination')}>
      <label className="page-pagination__size">
        <span>{t('common.pageControls.size')}</span>
        <select
          value={metadata.page_size}
          disabled={busy}
          onChange={(event) => onPageSizeChange(Number(event.target.value) as PageSize)}
        >
          {PAGE_SIZES.map((size) => (
            <option key={size} value={size}>
              {size}
            </option>
          ))}
        </select>
      </label>
      <span aria-live="polite">
        {t('common.pageControls.summary', {
          page: metadata.page,
          pages: metadata.total_pages,
          total: metadata.total_items,
        })}
      </span>
      <button
        type="button"
        className="btn btn-secondary"
        disabled={busy || current <= 1n}
        onClick={() => onPageChange((current - 1n).toString())}
      >
        {t('common.previous')}
      </button>
      <button
        type="button"
        className="btn btn-secondary"
        disabled={busy || current >= last}
        onClick={() => onPageChange((current + 1n).toString())}
      >
        {t('common.next')}
      </button>
      <label className="page-pagination__jump">
        <span>{t('common.pageControls.jump')}</span>
        <input
          inputMode="numeric"
          value={jump}
          maxLength={10}
          disabled={busy}
          aria-invalid={invalid}
          aria-describedby={invalid ? errorId : undefined}
          onChange={(event) => {
            setJump(event.target.value);
            setInvalid(false);
          }}
          onKeyDown={(event) => {
            if (event.key === 'Enter') {
              event.preventDefault();
              go();
            }
          }}
        />
      </label>
      <button type="button" className="btn btn-secondary" disabled={busy} onClick={go}>
        {t('common.pageControls.go')}
      </button>
      {invalid ? (
        <span id={errorId} className="field-error" role="alert">
          {t('common.pageControls.invalid')}
        </span>
      ) : null}
    </nav>
  );
}
