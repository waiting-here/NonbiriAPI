import { QueryFeedback } from '../QueryFeedback';
import { Button } from '@shared/components/ui/Button';
import { useState, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { FilterFieldLayoutContext } from './FieldLayoutContext';

export function FilterBar({
  search,
  query,
  as: Tag = 'form',
  persistKey,
  ariaLabel,
  secondary,
  secondaryLabel,
  secondaryAlwaysVisible = false,
  activeCount,
  footer,
  chips,
  chipsLabel,
  onClearAll,
  clearAllLabel,
  onSubmit,
  onReset,
  submitLabel,
  submitDisabled,
  resetDisabled,
  role,
}: {
  search: ReactNode;
  query?: {
    isFetching: boolean;
    data: unknown;
    isPlaceholderData?: boolean;
    error?: unknown;
    retained?: boolean;
  };
  as?: 'form' | 'div';
  persistKey?: string;
  ariaLabel?: string;
  secondary?: ReactNode;
  /** Defaults to “More filters”. */
  secondaryLabel?: string;
  secondaryAlwaysVisible?: boolean;
  activeCount: number;
  /** Actions or search feedback following all fields, including the disclosure. */
  footer?: ReactNode;
  chips?: readonly { key: string; label: ReactNode; onRemove: () => void; removeLabel: string }[];
  chipsLabel?: string;
  onClearAll?: () => void;
  clearAllLabel?: string;
  /** Omit for local filters that update the results as the user types. */
  onSubmit?: () => void;
  /**
   * When set, the bar renders its own Search and Reset buttons after the search slot, so
   * pages share one wording. Selects and toggles should still apply on change.
   */
  onReset?: () => void;
  /** Label of the built-in Search button. */
  submitLabel?: string;
  /** Disables Search while a request or input check runs. */
  submitDisabled?: boolean;
  /** Disables Reset during an in-flight request, independently of invalid input. */
  resetDisabled?: boolean;
  role?: 'search';
}) {
  const { t } = useTranslation();
  const storageKey = persistKey ? `nb.fold.${persistKey}` : null;
  const [open, setOpen] = useState(() => {
    try {
      return storageKey ? sessionStorage.getItem(storageKey) === '1' : false;
    } catch {
      return false;
    }
  });
  return (
    <FilterFieldLayoutContext value>
      <Tag
        role={role ?? (Tag === 'div' ? 'search' : undefined)}
        onKeyDown={(event) => {
          if (
            Tag === 'div' &&
            onSubmit &&
            event.key === 'Enter' &&
            !event.defaultPrevented &&
            !event.nativeEvent.isComposing &&
            event.nativeEvent.keyCode !== 229 &&
            (event.target as HTMLElement).tagName === 'INPUT'
          ) {
            event.preventDefault();
            event.stopPropagation();
            if (!submitDisabled) onSubmit();
          }
        }}
        className="nb-filter"
        aria-label={ariaLabel}
        onSubmit={(event) => {
          event.preventDefault();
          if (!submitDisabled) onSubmit?.();
        }}
      >
        <div className="nb-filter__row">
          <div className="nb-filter__search">
            {search}
            {onReset ? (
              <>
                {onSubmit && (
                  <Button
                    type={Tag === 'form' ? 'submit' : 'button'}
                    disabled={submitDisabled}
                    onClick={Tag === 'div' ? onSubmit : undefined}
                  >
                    {submitLabel ?? t('common.search')}
                  </Button>
                )}
                <Button
                  type="button"
                  variant="secondary"
                  disabled={resetDisabled}
                  onClick={onReset}
                >
                  {t('common.resetFilter')}
                </Button>
              </>
            ) : null}
          </div>
          {secondary && !secondaryAlwaysVisible ? (
            <details
              className="nb-filter__more"
              open={open}
              onToggle={(event) => {
                const next = event.currentTarget.open;
                setOpen(next);
                if (storageKey) {
                  try {
                    sessionStorage.setItem(storageKey, next ? '1' : '0');
                  } catch {
                    /* Disclosure remains usable without browser storage. */
                  }
                }
              }}
            >
              <summary className="nb-btn nb-btn--secondary">
                {secondaryLabel ?? t('common.moreFilters')}
                {activeCount ? ` · ${activeCount}` : ''}
              </summary>
              <div className="nb-filter__fields">{secondary}</div>
            </details>
          ) : null}
        </div>
        {query ? <QueryFeedback query={query} /> : null}
        {secondary && secondaryAlwaysVisible ? (
          <div className="nb-filter__fields">{secondary}</div>
        ) : null}
        {footer}
        {chips && chips.length ? (
          <div className="nb-chips" role={chipsLabel ? 'group' : undefined} aria-label={chipsLabel}>
            {chipsLabel && <span className="nb-small nb-muted">{chipsLabel}</span>}
            {chips.map((chip) => (
              <span key={chip.key} className="nb-chip">
                <span>{chip.label}</span>
                <button
                  type="button"
                  aria-label={chip.removeLabel}
                  onClick={(event) => {
                    const button = event.currentTarget;
                    if (button === document.activeElement) {
                      const item = button.parentElement;
                      const next =
                        item?.nextElementSibling?.querySelector('button') ??
                        item?.previousElementSibling?.querySelector('button') ??
                        button
                          .closest('.nb-filter')
                          ?.querySelector<HTMLElement>('input, select, button');
                      next?.focus();
                    }
                    chip.onRemove();
                  }}
                >
                  ×
                </button>
              </span>
            ))}
            {onClearAll ? (
              <Button type="button" variant="ghost" size="sm" onClick={onClearAll}>
                {clearAllLabel}
              </Button>
            ) : null}
          </div>
        ) : null}
      </Tag>
    </FilterFieldLayoutContext>
  );
}
