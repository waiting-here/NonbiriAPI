import { useId, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  isPageNumber,
  isPageSize,
  MAX_PAGE,
  PAGE_SIZES,
  type PageMetadata,
  type PageSize,
} from './pageNumbers';
import './pagePagination.css';

interface PagePaginationProps {
  metadata: PageMetadata;
  requestedPage?: string;
  onPageChange: (page: string) => void;
  onPageSizeChange: (size: PageSize) => void;
  busy?: boolean;
  /** Some existing domain protocols accept the full positive int64 range. */
  maxPage?: bigint;
  pageSizes?: readonly PageSize[];
}

function windowPages(current: bigint, last: bigint): (bigint | 'gap')[] {
  if (last <= 7n) return Array.from({ length: Number(last) }, (_, index) => BigInt(index + 1));
  const pages = [...new Set([1n, last, current - 1n, current, current + 1n])]
    .filter((page) => page >= 1n && page <= last)
    .sort((a, b) => (a < b ? -1 : 1));
  const result: (bigint | 'gap')[] = [];
  pages.forEach((page, index) => {
    if (index > 0 && page - pages[index - 1] > 1n) result.push('gap');
    result.push(page);
  });
  return result;
}

export function PagePagination(props: PagePaginationProps) {
  return <PagePaginationControls key={props.metadata.page} {...props} />;
}

function PagePaginationControls({
  metadata,
  onPageChange,
  onPageSizeChange,
  busy = false,
  requestedPage,
  maxPage = MAX_PAGE,
  pageSizes = PAGE_SIZES,
}: PagePaginationProps) {
  const { t } = useTranslation();
  const errorId = useId();
  const [jump, setJump] = useState(metadata.page);
  const [invalid, setInvalid] = useState(false);
  const total = BigInt(metadata.total_items);
  const current = BigInt(metadata.page);
  const last = BigInt(metadata.total_pages) > maxPage ? maxPage : BigInt(metadata.total_pages);
  const go = () => {
    if (busy) return;
    if (!isPageNumber(jump, maxPage)) {
      setInvalid(true);
      return;
    }
    const selected = BigInt(jump) > last ? last.toString() : jump;
    setJump(selected);
    setInvalid(false);
    onPageChange(selected);
  };
  if (total === 0n) return null;
  return (
    <nav className="pagination page-pagination nb-pager" aria-label={t('common.pagination')}>
      <span className="nb-pager__total" aria-live="polite">
        {t('common.pageControls.totalOnly', { total: metadata.total_items })}
      </span>
      {last > 1n ? (
        <span className="nb-pager__pages">
          <button
            type="button"
            aria-label={t('common.previous')}
            disabled={busy || current <= 1n}
            onClick={() => onPageChange((current - 1n).toString())}
          >
            ‹
          </button>
          {windowPages(current, last).map((page, index) =>
            page === 'gap' ? (
              <span key={`gap-${index}`} className="nb-pager__number" aria-hidden="true">
                …
              </span>
            ) : (
              <button
                key={page.toString()}
                type="button"
                className="nb-pager__number"
                aria-current={page === current ? 'page' : undefined}
                disabled={busy}
                onClick={() => onPageChange(page.toString())}
              >
                {page.toString()}
              </button>
            ),
          )}
          <span className="nb-pager__mobile" aria-live="polite">
            {t('common.pageControls.pageOf', { page: metadata.page, pages: metadata.total_pages })}
          </span>
          <button
            type="button"
            aria-label={t('common.next')}
            disabled={busy || current >= last}
            onClick={() => onPageChange((current + 1n).toString())}
          >
            ›
          </button>
          {last > 7n ? (
            <input
              className="nb-pager__jump nb-pager__number"
              inputMode="numeric"
              aria-label={t('common.pageControls.jump')}
              placeholder={t('common.pageControls.jumpShort')}
              value={jump}
              maxLength={maxPage.toString().length}
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
          ) : null}
        </span>
      ) : null}
      {total > BigInt(pageSizes.length ? Math.min(...pageSizes) : 10) ? (
        <label className="nb-pager__size">
          <span>{t('common.pageControls.sizeShort')}</span>
          <select
            aria-label={t('common.pageControls.size')}
            value={metadata.page_size}
            disabled={busy}
            onChange={(event) => {
              const size = Number(event.target.value);
              if (isPageSize(size) && pageSizes.includes(size)) onPageSizeChange(size);
            }}
          >
            {pageSizes.map((size) => (
              <option key={size} value={size}>
                {size}
              </option>
            ))}
          </select>
        </label>
      ) : null}
      {!busy && requestedPage !== undefined && requestedPage !== metadata.page ? (
        <span className="nb-pager__notice" role="status">
          {t('common.pageControls.clamped', { page: metadata.page })}
        </span>
      ) : null}
      {invalid ? (
        <span id={errorId} className="field-error nb-pager__notice" role="alert">
          {t('common.pageControls.invalid')}
        </span>
      ) : null}
    </nav>
  );
}
